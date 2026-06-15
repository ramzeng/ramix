package ramix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type webSocketTestFixture struct {
	server          *Server
	httpServer      *httptest.Server
	connections     chan *WebSocketConnection
	upgradeErrors   chan error
	configure       func(*WebSocketConnection)
	applicationBusy atomic.Int32
	maxApplication  atomic.Int32
}

func newWebSocketTestFixture(
	t *testing.T,
	configure func(*WebSocketConnection),
	options ...ServerOption,
) *webSocketTestFixture {
	t.Helper()
	options = append(options,
		WithWorkerCount(1),
		WithWorkerQueueCapacity(32),
		WithHeartbeatInterval(time.Hour),
		WithHeartbeatTimeout(2*time.Hour),
	)
	server, err := NewServer(options...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	server.workerPool.start()

	fixture := &webSocketTestFixture{
		server:        server,
		connections:   make(chan *WebSocketConnection, 8),
		upgradeErrors: make(chan error, 8),
		configure:     configure,
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	fixture.httpServer = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		socket, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			fixture.upgradeErrors <- err
			return
		}

		connectionID := atomic.AddUint64(&server.currentConnectionID, 1)
		base, err := newNetConnection(connectionID, server, TransportWebSocket, socket, func(data []byte) error {
			active := fixture.applicationBusy.Add(1)
			for {
				maximum := fixture.maxApplication.Load()
				if active <= maximum || fixture.maxApplication.CompareAndSwap(maximum, active) {
					break
				}
			}
			defer fixture.applicationBusy.Add(-1)
			return socket.WriteMessage(websocket.BinaryMessage, data)
		})
		if err != nil {
			_ = socket.Close()
			fixture.upgradeErrors <- err
			return
		}

		connection := &WebSocketConnection{netConnection: base, socket: socket}
		if fixture.configure != nil {
			fixture.configure(connection)
		}
		server.connectionManager.addConnection(connection)
		connection.open()
		fixture.connections <- connection
	}))

	t.Cleanup(func() {
		for _, connection := range server.connectionManager.snapshot() {
			connection.requestClose(OperationRead, errNetTestClosed)
		}
		waitCtx, cancelWait := context.WithTimeout(context.Background(), time.Second)
		defer cancelWait()
		if err := server.connectionManager.waitAll(waitCtx); err != nil {
			t.Errorf("connection cleanup error = %v", err)
		}

		drainCtx, cancelDrain := context.WithTimeout(context.Background(), time.Second)
		defer cancelDrain()
		if err := server.workerPool.stopAcceptingAndDrain(drainCtx); err != nil {
			t.Errorf("worker pool cleanup error = %v", err)
		}
		fixture.httpServer.Close()
	})

	return fixture
}

var errNetTestClosed = errors.New("test connection closed")

func (f *webSocketTestFixture) dial(t *testing.T) (*WebSocketConnection, *websocket.Conn) {
	t.Helper()
	url := "ws" + strings.TrimPrefix(f.httpServer.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("websocket dial error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	select {
	case connection := <-f.connections:
		return connection, client
	case err := <-f.upgradeErrors:
		t.Fatalf("websocket server setup error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket connection")
	}
	return nil, nil
}

func TestWebSocketConnectionBinaryMessageRoutes(t *testing.T) {
	fixture := newWebSocketTestFixture(t, nil)
	routed := make(chan string, 1)
	if err := fixture.server.RegisterRoute(21, func(ctx *Context) {
		routed <- string(ctx.Request.Message.Body)
	}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	_, client := fixture.dial(t)

	if err := client.WriteMessage(websocket.BinaryMessage, encodeTCPTestMessage(t, 21, "binary")); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	if got := waitForString(t, routed, "websocket binary route"); got != "binary" {
		t.Fatalf("routed body = %q, want binary", got)
	}
}

func TestWebSocketConnectionRejectsTextAndKeepsOtherConnectionAlive(t *testing.T) {
	fixture := newWebSocketTestFixture(t, nil)
	reported := make(chan tcpTestError, 2)
	fixture.server.connectionError = func(_ Connection, op ConnectionOperation, err error) {
		reported <- tcpTestError{op: op, err: err}
	}
	routed := make(chan string, 1)
	if err := fixture.server.RegisterRoute(22, func(ctx *Context) {
		routed <- string(ctx.Request.Message.Body)
	}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	badConnection, badClient := fixture.dial(t)
	healthyConnection, healthyClient := fixture.dial(t)

	if err := badClient.WriteMessage(websocket.TextMessage, encodeTCPTestMessage(t, 22, "text")); err != nil {
		t.Fatalf("WriteMessage(text) error = %v", err)
	}
	report := waitForTCPError(t, reported, "websocket text protocol error")
	if report.op != OperationProtocol || !errors.Is(report.err, ErrInvalidFrame) {
		t.Fatalf("reported error = (%q, %v), want (%q, %v)", report.op, report.err, OperationProtocol, ErrInvalidFrame)
	}
	waitCtx, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	if err := badConnection.wait(waitCtx); err != nil {
		t.Fatalf("bad connection wait error = %v", err)
	}

	if got := healthyConnection.connectionState(); got != connectionOpen {
		t.Fatalf("healthy connection state = %v, want open", got)
	}
	if err := healthyClient.WriteMessage(websocket.BinaryMessage, encodeTCPTestMessage(t, 22, "healthy")); err != nil {
		t.Fatalf("WriteMessage(healthy) error = %v", err)
	}
	if got := waitForString(t, routed, "healthy websocket route"); got != "healthy" {
		t.Fatalf("healthy routed body = %q, want healthy", got)
	}
}

func TestWebSocketConnectionMalformedBinaryClosesOnlyOffender(t *testing.T) {
	tests := []struct {
		name    string
		options []ServerOption
		payload []byte
		wantErr error
	}{
		{name: "empty", payload: []byte{}, wantErr: ErrInvalidFrame},
		{name: "incomplete", payload: []byte{1, 2, 3}, wantErr: ErrInvalidFrame},
		{name: "oversized", options: []ServerOption{WithServerMaxFrameLength(12)}, payload: encodeTCPTestMessage(t, 23, "too-large"), wantErr: ErrFrameTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newWebSocketTestFixture(t, nil, tt.options...)
			reported := make(chan tcpTestError, 2)
			fixture.server.connectionError = func(_ Connection, op ConnectionOperation, err error) {
				reported <- tcpTestError{op: op, err: err}
			}
			routed := make(chan string, 1)
			if err := fixture.server.RegisterRoute(23, func(ctx *Context) {
				routed <- string(ctx.Request.Message.Body)
			}); err != nil {
				t.Fatalf("RegisterRoute() error = %v", err)
			}
			badConnection, badClient := fixture.dial(t)
			healthyConnection, healthyClient := fixture.dial(t)

			if err := badClient.WriteMessage(websocket.BinaryMessage, tt.payload); err != nil {
				t.Fatalf("WriteMessage(bad) error = %v", err)
			}
			report := waitForTCPError(t, reported, "websocket malformed protocol error")
			if report.op != OperationProtocol || !errors.Is(report.err, tt.wantErr) {
				t.Fatalf("reported error = (%q, %v), want (%q, %v)", report.op, report.err, OperationProtocol, tt.wantErr)
			}
			waitCtx, cancelWait := context.WithTimeout(context.Background(), time.Second)
			defer cancelWait()
			if err := badConnection.wait(waitCtx); err != nil {
				t.Fatalf("bad connection wait error = %v", err)
			}

			if got := healthyConnection.connectionState(); got != connectionOpen {
				t.Fatalf("healthy connection state = %v, want open", got)
			}
			if err := healthyClient.WriteMessage(websocket.BinaryMessage, encodeTCPTestMessage(t, 23, "ok")); err != nil {
				t.Fatalf("WriteMessage(healthy) error = %v", err)
			}
			if got := waitForString(t, routed, "healthy websocket route"); got != "ok" {
				t.Fatalf("healthy routed body = %q, want ok", got)
			}
		})
	}
}

func TestWebSocketConnectionPingAndPongRefreshActivity(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Unix(100, 0).UnixNano())
	fixture := newWebSocketTestFixture(t, func(connection *WebSocketConnection) {
		connection.activity = newActivityClock(func() time.Time {
			return time.Unix(0, now.Load())
		})
		connection.refreshActivity()
	})
	connection, client := fixture.dial(t)

	pingTime := time.Unix(200, 0)
	now.Store(pingTime.UnixNano())
	if err := client.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("WriteControl(ping) error = %v", err)
	}
	waitForActivity(t, connection, pingTime, "ping activity")

	pongTime := time.Unix(300, 0)
	now.Store(pongTime.UnixNano())
	if err := client.WriteControl(websocket.PongMessage, []byte("pong"), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("WriteControl(pong) error = %v", err)
	}
	waitForActivity(t, connection, pongTime, "pong activity")
}

func TestWebSocketConnectionSerializesApplicationWritesWithControlWrites(t *testing.T) {
	fixture := newWebSocketTestFixture(t, nil)
	connection, client := fixture.dial(t)

	const messageCount = 20
	writeErrors := make(chan error, messageCount*2)
	var writers sync.WaitGroup
	for i := 0; i < messageCount; i++ {
		writers.Add(2)
		go func(index int) {
			defer writers.Done()
			writeErrors <- connection.Send(context.Background(), uint32(index+1), []byte("body"))
		}(i)
		go func(index int) {
			defer writers.Done()
			writeErrors <- connection.socket.PingHandler()(fmt.Sprintf("ping-%d", index))
		}(i)
	}

	for i := 0; i < messageCount; i++ {
		if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatalf("SetReadDeadline() error = %v", err)
		}
		messageType, _, err := client.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage(%d) error = %v", i, err)
		}
		if messageType != websocket.BinaryMessage {
			t.Fatalf("message type = %d, want binary", messageType)
		}
	}
	writers.Wait()
	close(writeErrors)
	for err := range writeErrors {
		if err != nil {
			t.Fatalf("concurrent websocket write error = %v", err)
		}
	}
	if got := fixture.maxApplication.Load(); got != 1 {
		t.Fatalf("maximum concurrent application writes = %d, want 1", got)
	}
}

func TestWebSocketConnectionQuiesceAllowsAcceptedResponseWrite(t *testing.T) {
	fixture := newWebSocketTestFixture(t, nil)
	reported := make(chan tcpTestError, 1)
	fixture.server.connectionError = func(_ Connection, op ConnectionOperation, err error) {
		reported <- tcpTestError{op: op, err: err}
	}
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	if err := fixture.server.RegisterRoute(24, func(ctx *Context) {
		close(handlerStarted)
		<-releaseHandler
		_ = ctx.Connection.Send(ctx, 25, []byte("response"))
	}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	connection, client := fixture.dial(t)

	if err := client.WriteMessage(websocket.BinaryMessage, encodeTCPTestMessage(t, 24, "request")); err != nil {
		t.Fatalf("WriteMessage(request) error = %v", err)
	}
	waitForSignal(t, handlerStarted, "websocket handler start")
	if err := connection.quiesceReads(); err != nil {
		t.Fatalf("quiesceReads() error = %v", err)
	}
	close(releaseHandler)

	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	messageType, payload, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage(response) error = %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("response type = %d, want binary", messageType)
	}
	message, err := (&Decoder{}).Decode(payload)
	if err != nil {
		t.Fatalf("Decode(response) error = %v", err)
	}
	if message.Event != 25 || string(message.Body) != "response" {
		t.Fatalf("response = (%d, %q), want (25, response)", message.Event, message.Body)
	}

	drainCtx, cancelDrain := context.WithTimeout(context.Background(), time.Second)
	defer cancelDrain()
	if err := connection.stopSendsAndDrain(drainCtx); err != nil {
		t.Fatalf("stopSendsAndDrain() error = %v", err)
	}
	waitCtx, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	if err := connection.wait(waitCtx); err != nil {
		t.Fatalf("wait() error = %v", err)
	}
	select {
	case report := <-reported:
		t.Fatalf("unexpected quiesce error report = (%q, %v)", report.op, report.err)
	default:
	}
}

func waitForActivity(t *testing.T, connection *WebSocketConnection, want time.Time, label string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if connection.activity.lastActive.Load() == want.UnixNano() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s: got %d, want %d", label, connection.activity.lastActive.Load(), want.UnixNano())
}
