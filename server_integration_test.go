package ramix

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

const integrationTimeout = 3 * time.Second

type integrationError struct {
	operation ConnectionOperation
	err       error
}

func startIntegrationServer(t *testing.T, server *Server, transport Transport) net.Addr {
	t.Helper()
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	var runErr error
	t.Cleanup(func() {
		cancelRun()
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), integrationTimeout)
		defer cancelShutdown()
		if err := server.Shutdown(shutdownCtx); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
		select {
		case <-runDone:
			if runErr != nil {
				t.Errorf("Run() error = %v", runErr)
			}
		case <-shutdownCtx.Done():
			t.Errorf("Run() did not return after shutdown: %v", shutdownCtx.Err())
		}
	})
	go func() {
		runErr = server.Run(runCtx)
		close(runDone)
	}()

	deadline := time.NewTimer(integrationTimeout)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()

	for {
		select {
		case <-runDone:
			t.Fatalf("Run() returned before readiness: %v", runErr)
		case <-ticker.C:
			if server.currentState() != stateRunning {
				continue
			}
			address := server.Address(transport)
			if address == nil {
				continue
			}
			return address
		case <-deadline.C:
			t.Fatalf("server did not reach running state within %s", integrationTimeout)
		}
	}
}

func newTCPIntegrationServer(t *testing.T, options ...ServerOption) *Server {
	t.Helper()
	options = append(options,
		WithTransports(TransportTCP),
		WithIPVersion("tcp4"),
		WithIP("127.0.0.1"),
		WithPort(0),
		WithHeartbeatInterval(time.Hour),
		WithHeartbeatTimeout(2*time.Hour),
	)
	server, err := NewServer(options...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}

func registerIntegrationEcho(t *testing.T, server *Server, requestEvent, responseEvent uint32) {
	t.Helper()
	if err := server.RegisterRoute(requestEvent, func(ctx *Context) {
		_ = ctx.Connection.Send(ctx, responseEvent, append([]byte("echo:"), ctx.Request.Message.Body...))
	}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
}

func encodeIntegrationMessage(t *testing.T, event uint32, body string) []byte {
	t.Helper()
	encoded, err := (&Encoder{}).Encode(Message{Event: event, Body: []byte(body)})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	return encoded
}

func readIntegrationMessage(reader io.Reader) (Message, error) {
	header := make([]byte, 8)
	if _, err := io.ReadFull(reader, header); err != nil {
		return Message{}, err
	}
	bodyLength := binary.LittleEndian.Uint32(header[4:8])
	frame := make([]byte, 8+int(bodyLength))
	copy(frame, header)
	if _, err := io.ReadFull(reader, frame[8:]); err != nil {
		return Message{}, err
	}
	return (&Decoder{}).Decode(frame)
}

func assertIntegrationMessage(t *testing.T, message Message, event uint32, body string) {
	t.Helper()
	if message.Event != event || string(message.Body) != body {
		t.Fatalf("message = (%d, %q), want (%d, %q)", message.Event, message.Body, event, body)
	}
}

func dialTCPIntegration(t *testing.T, address net.Addr) net.Conn {
	t.Helper()
	dialer := net.Dialer{Timeout: integrationTimeout}
	connection, err := dialer.Dial("tcp", address.String())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

func setIntegrationDeadline(t *testing.T, connection interface{ SetDeadline(time.Time) error }) {
	t.Helper()
	if err := connection.SetDeadline(time.Now().Add(integrationTimeout)); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}
}

func TestIntegration_TCPRequestResponse(t *testing.T) {
	server := newTCPIntegrationServer(t)
	registerIntegrationEcho(t, server, 1, 101)
	address := startIntegrationServer(t, server, TransportTCP)

	client := dialTCPIntegration(t, address)
	setIntegrationDeadline(t, client)
	if _, err := client.Write(encodeIntegrationMessage(t, 1, "hello")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	response, err := readIntegrationMessage(client)
	if err != nil {
		t.Fatalf("readIntegrationMessage() error = %v", err)
	}
	assertIntegrationMessage(t, response, 101, "echo:hello")
}

func TestIntegration_TCPSplitFrame(t *testing.T) {
	server := newTCPIntegrationServer(t)
	registerIntegrationEcho(t, server, 2, 102)
	address := startIntegrationServer(t, server, TransportTCP)

	client := dialTCPIntegration(t, address)
	setIntegrationDeadline(t, client)
	frame := encodeIntegrationMessage(t, 2, "split")
	if _, err := client.Write(frame[:5]); err != nil {
		t.Fatalf("Write(first) error = %v", err)
	}
	if _, err := client.Write(frame[5:]); err != nil {
		t.Fatalf("Write(second) error = %v", err)
	}
	response, err := readIntegrationMessage(client)
	if err != nil {
		t.Fatalf("readIntegrationMessage() error = %v", err)
	}
	assertIntegrationMessage(t, response, 102, "echo:split")
}

func TestIntegration_TCPCoalescedFramesPreserveOrder(t *testing.T) {
	server := newTCPIntegrationServer(t)
	registerIntegrationEcho(t, server, 3, 103)
	address := startIntegrationServer(t, server, TransportTCP)

	client := dialTCPIntegration(t, address)
	setIntegrationDeadline(t, client)
	first := encodeIntegrationMessage(t, 3, "first")
	second := encodeIntegrationMessage(t, 3, "second")
	if _, err := client.Write(append(first, second...)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	for _, want := range []string{"echo:first", "echo:second"} {
		response, err := readIntegrationMessage(client)
		if err != nil {
			t.Fatalf("readIntegrationMessage() error = %v", err)
		}
		assertIntegrationMessage(t, response, 103, want)
	}
}

func TestIntegration_TCPMalformedClientIsIsolated(t *testing.T) {
	server := newTCPIntegrationServer(t)
	registerIntegrationEcho(t, server, 4, 104)
	address := startIntegrationServer(t, server, TransportTCP)

	malformed := dialTCPIntegration(t, address)
	healthy := dialTCPIntegration(t, address)
	setIntegrationDeadline(t, malformed)
	setIntegrationDeadline(t, healthy)
	invalid := make([]byte, 8)
	binary.LittleEndian.PutUint32(invalid[4:8], ^uint32(0))
	if _, err := malformed.Write(invalid); err != nil {
		t.Fatalf("malformed Write() error = %v", err)
	}
	buffer := make([]byte, 1)
	_, err := malformed.Read(buffer)
	assertIntegrationConnectionClosed(t, err)

	if _, err := healthy.Write(encodeIntegrationMessage(t, 4, "healthy")); err != nil {
		t.Fatalf("healthy Write() error = %v", err)
	}
	response, err := readIntegrationMessage(healthy)
	if err != nil {
		t.Fatalf("healthy read error = %v", err)
	}
	assertIntegrationMessage(t, response, 104, "echo:healthy")
}

func TestIntegration_TCPConcurrentClientsPreservePerConnectionOrder(t *testing.T) {
	server := newTCPIntegrationServer(t, WithWorkerCount(4), WithWorkerQueueCapacity(64))
	registerIntegrationEcho(t, server, 5, 105)
	address := startIntegrationServer(t, server, TransportTCP)

	const clientCount = 6
	const messagesPerClient = 12
	errCh := make(chan error, clientCount)
	var wg sync.WaitGroup
	for clientIndex := 0; clientIndex < clientCount; clientIndex++ {
		client := dialTCPIntegration(t, address)
		clientIndex := clientIndex
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer client.Close()
			if err := client.SetDeadline(time.Now().Add(integrationTimeout)); err != nil {
				errCh <- err
				return
			}
			var frames []byte
			for messageIndex := 0; messageIndex < messagesPerClient; messageIndex++ {
				body := fmt.Sprintf("%d:%02d", clientIndex, messageIndex)
				encoded, err := (&Encoder{}).Encode(Message{Event: 5, Body: []byte(body)})
				if err != nil {
					errCh <- err
					return
				}
				frames = append(frames, encoded...)
			}
			if _, err := client.Write(frames); err != nil {
				errCh <- err
				return
			}
			for messageIndex := 0; messageIndex < messagesPerClient; messageIndex++ {
				message, err := readIntegrationMessage(client)
				if err != nil {
					errCh <- err
					return
				}
				want := fmt.Sprintf("echo:%d:%02d", clientIndex, messageIndex)
				if message.Event != 105 || string(message.Body) != want {
					errCh <- fmt.Errorf("client %d response = (%d, %q), want (105, %q)", clientIndex, message.Event, message.Body, want)
					return
				}
			}
		}()
	}
	waitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(integrationTimeout):
		t.Fatal("concurrent clients did not finish before deadline")
	}
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestIntegration_TCPMaximumConnectionsRejectsExtraClient(t *testing.T) {
	server := newTCPIntegrationServer(t, WithMaxConnectionsCount(1))
	registerIntegrationEcho(t, server, 6, 106)
	opened := make(chan struct{}, 1)
	if err := server.OnConnectionOpen(func(Connection) { opened <- struct{}{} }); err != nil {
		t.Fatalf("OnConnectionOpen() error = %v", err)
	}
	address := startIntegrationServer(t, server, TransportTCP)

	first := dialTCPIntegration(t, address)
	setIntegrationDeadline(t, first)
	select {
	case <-opened:
	case <-time.After(integrationTimeout):
		t.Fatal("first connection was not registered")
	}

	second := dialTCPIntegration(t, address)
	setIntegrationDeadline(t, second)
	if _, err := second.Write(encodeIntegrationMessage(t, 6, "rejected")); err != nil {
		assertIntegrationConnectionClosed(t, err)
	} else {
		buffer := make([]byte, 1)
		_, err := second.Read(buffer)
		assertIntegrationConnectionClosed(t, err)
	}

	if _, err := first.Write(encodeIntegrationMessage(t, 6, "existing")); err != nil {
		t.Fatalf("existing Write() error = %v", err)
	}
	response, err := readIntegrationMessage(first)
	if err != nil {
		t.Fatalf("existing read error = %v", err)
	}
	assertIntegrationMessage(t, response, 106, "echo:existing")
}

func TestIntegration_TCPTLSRequestResponse(t *testing.T) {
	server := newTCPIntegrationServer(t,
		WithCertFile("examples/tls/public_certificate.pem"),
		WithPrivateKeyFile("examples/tls/private_key.pem"),
	)
	registerIntegrationEcho(t, server, 7, 107)
	address := startIntegrationServer(t, server, TransportTCP)

	// #nosec G402 -- test fixture intentionally bypasses certificate verification.
	config := &tls.Config{InsecureSkipVerify: true}
	dialer := net.Dialer{Timeout: integrationTimeout}
	client, err := tls.DialWithDialer(&dialer, "tcp", address.String(), config)
	if err != nil {
		t.Fatalf("tls.DialWithDialer() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	setIntegrationDeadline(t, client)
	if _, err := client.Write(encodeIntegrationMessage(t, 7, "secure")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	response, err := readIntegrationMessage(client)
	if err != nil {
		t.Fatalf("readIntegrationMessage() error = %v", err)
	}
	assertIntegrationMessage(t, response, 107, "echo:secure")
}

func waitForIntegrationError(t *testing.T, errorsCh <-chan integrationError) integrationError {
	t.Helper()
	select {
	case reported := <-errorsCh:
		return reported
	case <-time.After(integrationTimeout):
		t.Fatal("timed out waiting for connection error")
		return integrationError{}
	}
}

func assertIntegrationConnectionClosed(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("connection remained open")
	}
	var netError net.Error
	if errors.As(err, &netError) && netError.Timeout() {
		t.Fatalf("connection did not close before deadline: %v", err)
	}
}
