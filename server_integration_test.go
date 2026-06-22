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

type integrationServerRun struct {
	done chan struct{}
	mu   sync.Mutex
	err  error
}

func (r *integrationServerRun) setResult(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
}

func (r *integrationServerRun) result() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func startIntegrationServer(t *testing.T, server *Server, transport Transport) net.Addr {
	t.Helper()
	address, _ := startIntegrationServerWithContext(t, server, transport, context.Background())
	return address
}

func startIntegrationServerWithContext(
	t *testing.T,
	server *Server,
	transport Transport,
	ctx context.Context,
) (net.Addr, *integrationServerRun) {
	t.Helper()
	runCtx, cancelRun := context.WithCancel(ctx)
	run := &integrationServerRun{done: make(chan struct{})}
	t.Cleanup(func() {
		cancelRun()
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), integrationTimeout)
		defer cancelShutdown()
		if err := server.Shutdown(shutdownCtx); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
		select {
		case <-run.done:
			if err := run.result(); err != nil {
				t.Errorf("Run() error = %v", err)
			}
		case <-shutdownCtx.Done():
			t.Errorf("Run() did not return after shutdown: %v", shutdownCtx.Err())
		}
	})
	go func() {
		run.setResult(server.Run(runCtx))
		close(run.done)
	}()

	deadline := time.NewTimer(integrationTimeout)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()

	for {
		select {
		case <-run.done:
			t.Fatalf("Run() returned before readiness: %v", run.result())
		case <-ticker.C:
			if server.currentState() != stateRunning {
				continue
			}
			address := server.Address(transport)
			if address == nil {
				continue
			}
			return address, run
		case <-deadline.C:
			t.Fatalf("server did not reach running state within %s", integrationTimeout)
		}
	}
}

func waitForIntegrationRun(t *testing.T, run *integrationServerRun) error {
	t.Helper()
	select {
	case <-run.done:
		return run.result()
	case <-time.After(integrationTimeout):
		t.Fatal("timed out waiting for Run() completion")
		return nil
	}
}

func waitForIntegrationShutdown(t *testing.T, shutdownDone <-chan error) error {
	t.Helper()
	select {
	case err := <-shutdownDone:
		return err
	case <-time.After(integrationTimeout):
		t.Fatal("timed out waiting for Shutdown() completion")
		return nil
	}
}

func waitForIntegrationResult(t *testing.T, results <-chan error, label string) error {
	t.Helper()
	select {
	case err := <-results:
		return err
	case <-time.After(integrationTimeout):
		t.Fatalf("timed out waiting for %s", label)
		return nil
	}
}

func waitForIntegrationStats(t *testing.T, server *Server, condition func(ServerStats) bool, label string) ServerStats {
	t.Helper()
	deadline := time.Now().Add(integrationTimeout)
	for time.Now().Before(deadline) {
		stats := server.Stats()
		if condition(stats) {
			return stats
		}
		time.Sleep(time.Millisecond)
	}
	stats := server.Stats()
	t.Fatalf("timed out waiting for %s; final Stats() = %+v", label, stats)
	return ServerStats{}
}

func waitForTCPListenerClosed(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(integrationTimeout)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err != nil {
			return
		}
		_ = connection.Close()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("TCP listener continued accepting connections during shutdown")
}

func assertIntegrationShutdownInvariants(t *testing.T, server *Server) {
	t.Helper()
	if got := server.connectionManager.connectionsCount(); got != 0 {
		t.Fatalf("connection registry count = %d, want 0", got)
	}
	if got := len(server.connectionManager.finalizationSnapshot()); got != 0 {
		t.Fatalf("finalizing connection count = %d, want 0", got)
	}
	for index, worker := range server.workerPool.workers {
		select {
		case <-worker.done:
		default:
			t.Fatalf("worker %d completion channel remains open", index)
		}
	}
	servicesDone := make(chan struct{})
	go func() {
		server.serviceWG.Wait()
		close(servicesDone)
	}()
	select {
	case <-servicesDone:
	case <-time.After(integrationTimeout):
		t.Fatal("serving goroutines did not finish")
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

func TestIntegration_TCPStatisticsSnapshot(t *testing.T) {
	server := newTCPIntegrationServer(t)
	if err := server.RegisterRoute(9, func(ctx *Context) {
		time.Sleep(5 * time.Millisecond)
		_ = ctx.Connection.Send(ctx, 109, append([]byte("echo:"), ctx.Request.Message.Body...))
	}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	address, run := startIntegrationServerWithContext(t, server, TransportTCP, context.Background())

	client := dialTCPIntegration(t, address)
	setIntegrationDeadline(t, client)
	active := waitForIntegrationStats(t, server, func(stats ServerStats) bool {
		return stats.TCP.ActiveConnections == 1
	}, "one active TCP connection")
	if active.WebSocket != (TransportStats{}) {
		t.Fatalf("WebSocket stats after TCP dial = %+v, want zero", active.WebSocket)
	}
	if got := active.Total.ActiveConnections; got != 1 {
		t.Fatalf("Total.ActiveConnections after TCP dial = %d, want 1", got)
	}

	if _, err := client.Write(encodeIntegrationMessage(t, 9, "hello")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	response, err := readIntegrationMessage(client)
	if err != nil {
		t.Fatalf("readIntegrationMessage() error = %v", err)
	}
	assertIntegrationMessage(t, response, 109, "echo:hello")

	stats := waitForIntegrationStats(t, server, func(stats ServerStats) bool {
		return stats.TCP.ReceivedMessages == 1 &&
			stats.TCP.ReceivedBytes == 5 &&
			stats.TCP.SentMessages == 1 &&
			stats.TCP.SentBytes == 10 &&
			stats.TCP.CompletedRequests == 1 &&
			stats.TCP.QueuedTasks == 0 &&
			stats.TCP.TotalRequestDuration >= 5*time.Millisecond &&
			stats.TCP.MaximumRequestDuration == stats.TCP.TotalRequestDuration
	}, "TCP request statistics")
	if stats.TCP.ActiveConnections != 1 {
		t.Fatalf("TCP.ActiveConnections after request = %d, want 1", stats.TCP.ActiveConnections)
	}
	if stats.WebSocket != (TransportStats{}) {
		t.Fatalf("WebSocket stats after TCP request = %+v, want zero", stats.WebSocket)
	}
	if stats.Total != stats.TCP {
		t.Fatalf("Total stats after TCP request = %+v, want TCP stats %+v", stats.Total, stats.TCP)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("client Close() error = %v", err)
	}
	closedStats := waitForIntegrationStats(t, server, func(stats ServerStats) bool {
		return stats.TCP.ActiveConnections == 0 && stats.Total.ActiveConnections == 0
	}, "TCP active connection gauge to return to zero")
	wantClosed := stats
	wantClosed.TCP.ActiveConnections = 0
	wantClosed.Total.ActiveConnections = 0
	if closedStats != wantClosed {
		t.Fatalf("Stats() after TCP client close = %+v, want %+v", closedStats, wantClosed)
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := waitForIntegrationRun(t, run); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := server.Stats(); got != closedStats {
		t.Fatalf("Stats() after TCP shutdown = %+v, want preserved snapshot %+v", got, closedStats)
	}
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
	stats := waitForIntegrationStats(t, server, func(stats ServerStats) bool {
		return stats.TCP.ConnectionErrors >= 1
	}, "TCP connection error after malformed client closes")
	if stats.WebSocket.ConnectionErrors != 0 {
		t.Fatalf("WebSocket.ConnectionErrors = %d, want 0", stats.WebSocket.ConnectionErrors)
	}
	if stats.Total.ConnectionErrors != stats.TCP.ConnectionErrors {
		t.Fatalf("Total.ConnectionErrors = %d, want TCP.ConnectionErrors %d", stats.Total.ConnectionErrors, stats.TCP.ConnectionErrors)
	}

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
	statsStop := make(chan struct{})
	statsDone := make(chan struct{})
	go func() {
		defer close(statsDone)
		for {
			select {
			case <-statsStop:
				return
			default:
				_ = server.Stats()
				time.Sleep(time.Microsecond)
			}
		}
	}()
	var stopStatsOnce sync.Once
	stopStatsPoller := func() {
		close(statsStop)
		<-statsDone
	}
	defer stopStatsOnce.Do(stopStatsPoller)

	for clientIndex := 0; clientIndex < clientCount; clientIndex++ {
		client := dialTCPIntegration(t, address)
		clientIndex := clientIndex
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { _ = client.Close() }()
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
	stopStatsOnce.Do(stopStatsPoller)
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

func TestIntegration_TCPShutdownDrainsAcceptedResponse(t *testing.T) {
	server := newTCPIntegrationServer(t, WithShutdownTimeout(time.Second))
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseHandler) }) }
	sendDone := make(chan error, 1)
	if err := server.RegisterRoute(8, func(ctx *Context) {
		close(handlerStarted)
		<-releaseHandler
		sendDone <- ctx.Connection.Send(ctx, 108, []byte("drained"))
	}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	address, run := startIntegrationServerWithContext(t, server, TransportTCP, context.Background())
	// Registered after server cleanup so LIFO cleanup releases the handler first.
	t.Cleanup(release)
	client := dialTCPIntegration(t, address)
	setIntegrationDeadline(t, client)
	if _, err := client.Write(encodeIntegrationMessage(t, 8, "request")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	select {
	case <-handlerStarted:
	case <-time.After(integrationTimeout):
		t.Fatal("handler did not start")
	}

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- server.Shutdown(context.Background()) }()
	waitForServerState(t, server, stateStopping)
	waitForTCPListenerClosed(t, address.String())
	release()
	response, err := readIntegrationMessage(client)
	if err != nil {
		t.Fatalf("read drained response error = %v", err)
	}
	assertIntegrationMessage(t, response, 108, "drained")
	if err := waitForIntegrationResult(t, sendDone, "handler Send()"); err != nil {
		t.Fatalf("handler Send() error = %v", err)
	}
	if err := waitForIntegrationShutdown(t, shutdownDone); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := waitForIntegrationRun(t, run); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(integrationTimeout)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	buffer := make([]byte, 1)
	_, err = client.Read(buffer)
	assertIntegrationConnectionClosed(t, err)
	assertIntegrationShutdownInvariants(t, server)
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
