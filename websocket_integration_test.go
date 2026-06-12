package ramix

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func newWebSocketIntegrationServer(t *testing.T, options ...ServerOption) *Server {
	t.Helper()
	options = append(options,
		WithTransports(TransportWebSocket),
		WithIP("127.0.0.1"),
		WithWebSocketPort(0),
		WithWebSocketPath("/integration"),
		WithHeartbeatInterval(time.Hour),
		WithHeartbeatTimeout(2*time.Hour),
	)
	server, err := NewServer(options...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}

func webSocketIntegrationURL(server *Server, address string, secure bool) string {
	scheme := "ws"
	if secure {
		scheme = "wss"
	}
	return (&url.URL{Scheme: scheme, Host: address, Path: server.WebSocketPath}).String()
}

func dialWebSocketIntegration(t *testing.T, dialer *websocket.Dialer, rawURL string) *websocket.Conn {
	t.Helper()
	if dialer == nil {
		dialer = &websocket.Dialer{HandshakeTimeout: integrationTimeout}
	}
	connection, response, err := dialer.Dial(rawURL, http.Header{})
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		t.Fatalf("websocket Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	deadline := time.Now().Add(integrationTimeout)
	if err := connection.SetReadDeadline(deadline); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if err := connection.SetWriteDeadline(deadline); err != nil {
		t.Fatalf("SetWriteDeadline() error = %v", err)
	}
	return connection
}

func readWebSocketIntegrationMessage(connection *websocket.Conn) (Message, error) {
	messageType, payload, err := connection.ReadMessage()
	if err != nil {
		return Message{}, err
	}
	if messageType != websocket.BinaryMessage {
		return Message{}, fmt.Errorf("message type = %d, want binary", messageType)
	}
	return (&Decoder{}).Decode(payload)
}

func waitForWebSocketListenerClosed(t *testing.T, rawURL string) {
	t.Helper()
	deadline := time.Now().Add(integrationTimeout)
	for time.Now().Before(deadline) {
		dialer := websocket.Dialer{HandshakeTimeout: 50 * time.Millisecond}
		connection, response, err := dialer.Dial(rawURL, http.Header{})
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if err != nil {
			return
		}
		_ = connection.Close()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("WebSocket listener continued accepting connections during shutdown")
}

func TestIntegration_WebSocketRequestResponse(t *testing.T) {
	server := newWebSocketIntegrationServer(t)
	registerIntegrationEcho(t, server, 11, 111)
	address := startIntegrationServer(t, server, TransportWebSocket)
	client := dialWebSocketIntegration(t, nil, webSocketIntegrationURL(server, address.String(), false))

	if err := client.WriteMessage(websocket.BinaryMessage, encodeIntegrationMessage(t, 11, "hello")); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	response, err := readWebSocketIntegrationMessage(client)
	if err != nil {
		t.Fatalf("readWebSocketIntegrationMessage() error = %v", err)
	}
	assertIntegrationMessage(t, response, 111, "echo:hello")
}

func TestIntegration_WebSocketTextClientIsIsolated(t *testing.T) {
	server := newWebSocketIntegrationServer(t)
	registerIntegrationEcho(t, server, 12, 112)
	address := startIntegrationServer(t, server, TransportWebSocket)
	rawURL := webSocketIntegrationURL(server, address.String(), false)

	invalid := dialWebSocketIntegration(t, nil, rawURL)
	healthy := dialWebSocketIntegration(t, nil, rawURL)
	if err := invalid.WriteMessage(websocket.TextMessage, []byte("not binary")); err != nil {
		t.Fatalf("WriteMessage(text) error = %v", err)
	}
	_, _, err := invalid.ReadMessage()
	assertIntegrationConnectionClosed(t, err)

	if err := healthy.WriteMessage(websocket.BinaryMessage, encodeIntegrationMessage(t, 12, "healthy")); err != nil {
		t.Fatalf("healthy WriteMessage() error = %v", err)
	}
	response, err := readWebSocketIntegrationMessage(healthy)
	if err != nil {
		t.Fatalf("healthy read error = %v", err)
	}
	assertIntegrationMessage(t, response, 112, "echo:healthy")
}

func TestIntegration_WebSocketMalformedBinaryClientIsIsolated(t *testing.T) {
	server := newWebSocketIntegrationServer(t)
	registerIntegrationEcho(t, server, 13, 113)
	address := startIntegrationServer(t, server, TransportWebSocket)
	rawURL := webSocketIntegrationURL(server, address.String(), false)

	invalid := dialWebSocketIntegration(t, nil, rawURL)
	healthy := dialWebSocketIntegration(t, nil, rawURL)
	if err := invalid.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("WriteMessage(malformed) error = %v", err)
	}
	_, _, err := invalid.ReadMessage()
	assertIntegrationConnectionClosed(t, err)

	if err := healthy.WriteMessage(websocket.BinaryMessage, encodeIntegrationMessage(t, 13, "healthy")); err != nil {
		t.Fatalf("healthy WriteMessage() error = %v", err)
	}
	response, err := readWebSocketIntegrationMessage(healthy)
	if err != nil {
		t.Fatalf("healthy read error = %v", err)
	}
	assertIntegrationMessage(t, response, 113, "echo:healthy")
}

func TestIntegration_WebSocketPingExtendsActivity(t *testing.T) {
	server, err := NewServer(
		WithTransports(TransportWebSocket),
		WithIP("127.0.0.1"),
		WithWebSocketPort(0),
		WithWebSocketPath("/integration"),
		WithHeartbeatInterval(40*time.Millisecond),
		WithHeartbeatTimeout(90*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	registerIntegrationEcho(t, server, 14, 114)
	address := startIntegrationServer(t, server, TransportWebSocket)
	client := dialWebSocketIntegration(t, nil, webSocketIntegrationURL(server, address.String(), false))

	for index := 0; index < 4; index++ {
		messageType := websocket.PingMessage
		if index%2 == 1 {
			messageType = websocket.PongMessage
		}
		if err := client.WriteControl(messageType, []byte("keepalive"), time.Now().Add(integrationTimeout)); err != nil {
			t.Fatalf("WriteControl(%d) error = %v", messageType, err)
		}
		time.Sleep(35 * time.Millisecond)
	}
	if err := client.WriteMessage(websocket.BinaryMessage, encodeIntegrationMessage(t, 14, "alive")); err != nil {
		t.Fatalf("WriteMessage() after pings error = %v", err)
	}
	response, err := readWebSocketIntegrationMessage(client)
	if err != nil {
		t.Fatalf("read after pings error = %v", err)
	}
	assertIntegrationMessage(t, response, 114, "echo:alive")
}

func TestIntegration_WebSocketTLSRequestResponse(t *testing.T) {
	server := newWebSocketIntegrationServer(t,
		WithCertFile("examples/tls/public_certificate.pem"),
		WithPrivateKeyFile("examples/tls/private_key.pem"),
	)
	registerIntegrationEcho(t, server, 15, 115)
	address := startIntegrationServer(t, server, TransportWebSocket)

	// #nosec G402 -- test fixture intentionally bypasses certificate verification.
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	dialer := &websocket.Dialer{
		HandshakeTimeout: integrationTimeout,
		TLSClientConfig:  tlsConfig,
	}
	client := dialWebSocketIntegration(t, dialer, webSocketIntegrationURL(server, address.String(), true))
	if err := client.WriteMessage(websocket.BinaryMessage, encodeIntegrationMessage(t, 15, "secure")); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	response, err := readWebSocketIntegrationMessage(client)
	if err != nil {
		t.Fatalf("readWebSocketIntegrationMessage() error = %v", err)
	}
	assertIntegrationMessage(t, response, 115, "echo:secure")
}

func TestIntegration_WebSocketShutdownDrainsAcceptedResponse(t *testing.T) {
	server := newWebSocketIntegrationServer(t, WithShutdownTimeout(time.Second))
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseHandler) }) }
	sendDone := make(chan error, 1)
	if err := server.RegisterRoute(18, func(ctx *Context) {
		close(handlerStarted)
		<-releaseHandler
		sendDone <- ctx.Connection.Send(ctx, 118, []byte("drained"))
	}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	address, run := startIntegrationServerWithContext(t, server, TransportWebSocket, context.Background())
	// Registered after server cleanup so LIFO cleanup releases the handler first.
	t.Cleanup(release)
	rawURL := webSocketIntegrationURL(server, address.String(), false)
	client := dialWebSocketIntegration(t, nil, rawURL)
	if err := client.WriteMessage(websocket.BinaryMessage, encodeIntegrationMessage(t, 18, "request")); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	select {
	case <-handlerStarted:
	case <-time.After(integrationTimeout):
		t.Fatal("handler did not start")
	}

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- server.Shutdown(context.Background()) }()
	waitForServerState(t, server, stateStopping)
	waitForWebSocketListenerClosed(t, rawURL)
	release()
	drained, err := readWebSocketIntegrationMessage(client)
	if err != nil {
		t.Fatalf("read drained response error = %v", err)
	}
	assertIntegrationMessage(t, drained, 118, "drained")
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
	_, _, err = client.ReadMessage()
	assertIntegrationConnectionClosed(t, err)
	assertIntegrationShutdownInvariants(t, server)
}

func TestIntegration_WebSocketWorkerQueueSaturationIsIsolated(t *testing.T) {
	server := newWebSocketIntegrationServer(t, WithWorkerCount(1), WithWorkerQueueCapacity(1))
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseHandler) }) }
	if err := server.RegisterRoute(16, func(ctx *Context) {
		select {
		case <-handlerStarted:
		default:
			close(handlerStarted)
		}
		<-releaseHandler
	}); err != nil {
		t.Fatalf("RegisterRoute(blocking) error = %v", err)
	}
	registerIntegrationEcho(t, server, 17, 117)
	reported := make(chan integrationError, 4)
	if err := server.OnConnectionError(func(_ Connection, operation ConnectionOperation, err error) {
		reported <- integrationError{operation: operation, err: err}
	}); err != nil {
		t.Fatalf("OnConnectionError() error = %v", err)
	}
	address := startIntegrationServer(t, server, TransportWebSocket)
	// Registered after server cleanup so LIFO cleanup releases the worker first.
	t.Cleanup(release)
	rawURL := webSocketIntegrationURL(server, address.String(), false)
	offending := dialWebSocketIntegration(t, nil, rawURL)
	healthy := dialWebSocketIntegration(t, nil, rawURL)

	if err := offending.WriteMessage(websocket.BinaryMessage, encodeIntegrationMessage(t, 16, "blocking")); err != nil {
		t.Fatalf("WriteMessage(blocking) error = %v", err)
	}
	select {
	case <-handlerStarted:
	case <-time.After(integrationTimeout):
		t.Fatal("blocking handler did not start")
	}
	if err := offending.WriteMessage(websocket.BinaryMessage, encodeIntegrationMessage(t, 16, "queued")); err != nil {
		t.Fatalf("WriteMessage(queued) error = %v", err)
	}
	if err := offending.WriteMessage(websocket.BinaryMessage, encodeIntegrationMessage(t, 16, "overflow")); err != nil {
		t.Fatalf("WriteMessage(overflow) error = %v", err)
	}

	queueError := waitForIntegrationError(t, reported)
	if queueError.operation != OperationTask || !errors.Is(queueError.err, ErrWorkerQueueFull) {
		t.Fatalf("reported error = (%q, %v), want (%q, ErrWorkerQueueFull)", queueError.operation, queueError.err, OperationTask)
	}
	_, _, err := offending.ReadMessage()
	assertIntegrationConnectionClosed(t, err)
	release()

	if err := healthy.WriteMessage(websocket.BinaryMessage, encodeIntegrationMessage(t, 17, "healthy")); err != nil {
		t.Fatalf("healthy WriteMessage() error = %v", err)
	}
	response, err := readWebSocketIntegrationMessage(healthy)
	if err != nil {
		t.Fatalf("healthy read error = %v", err)
	}
	assertIntegrationMessage(t, response, 117, "echo:healthy")
}
