package ramix

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type lifecycleTestAddr string

func (a lifecycleTestAddr) Network() string { return "test" }
func (a lifecycleTestAddr) String() string  { return string(a) }

type fakeLifecycleTransport struct {
	closeCount atomic.Int32
	closed     chan struct{}
	closeOnce  sync.Once

	readDeadlineCount atomic.Int32
	readQuiesced      chan struct{}
	readQuiesceOnce   sync.Once

	writeGate    <-chan struct{}
	writeStarted chan struct{}
	writeOnce    sync.Once
	writesMu     sync.Mutex
	writes       [][]byte
}

func newFakeLifecycleTransport() *fakeLifecycleTransport {
	return &fakeLifecycleTransport{
		closed:       make(chan struct{}),
		readQuiesced: make(chan struct{}),
	}
}

func (t *fakeLifecycleTransport) Close() error {
	t.closeOnce.Do(func() {
		t.closeCount.Add(1)
		close(t.closed)
	})
	return nil
}

func (t *fakeLifecycleTransport) RemoteAddr() net.Addr {
	return lifecycleTestAddr("remote")
}

func (t *fakeLifecycleTransport) SetReadDeadline(time.Time) error {
	t.readDeadlineCount.Add(1)
	t.readQuiesceOnce.Do(func() {
		close(t.readQuiesced)
	})
	return nil
}

func (t *fakeLifecycleTransport) Read([]byte) (int, error) {
	select {
	case <-t.closed:
		return 0, net.ErrClosed
	case <-t.readQuiesced:
		return 0, context.DeadlineExceeded
	}
}

func (t *fakeLifecycleTransport) Write(data []byte) error {
	if t.writeStarted != nil {
		t.writeOnce.Do(func() {
			close(t.writeStarted)
		})
	}

	if t.writeGate != nil {
		select {
		case <-t.writeGate:
		case <-t.closed:
			return net.ErrClosed
		}
	}

	t.writesMu.Lock()
	t.writes = append(t.writes, append([]byte(nil), data...))
	t.writesMu.Unlock()
	return nil
}

func (t *fakeLifecycleTransport) writtenMessages(tester *testing.T) []Message {
	tester.Helper()

	t.writesMu.Lock()
	defer t.writesMu.Unlock()

	messages := make([]Message, 0, len(t.writes))
	for _, data := range t.writes {
		message, err := (&Decoder{}).Decode(data)
		if err != nil {
			tester.Fatalf("Decode(write) error = %v", err)
		}
		messages = append(messages, message)
	}
	return messages
}

func newLifecycleTestConnection(t *testing.T, transport *fakeLifecycleTransport, queueCapacity uint32) (*Server, *netConnection) {
	t.Helper()

	server, err := NewServer(
		WithConnectionWriteBufferSize(queueCapacity),
		WithHeartbeatInterval(time.Hour),
		WithHeartbeatTimeout(2*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	connection, err := newNetConnection(1, server, transport, transport.Write)
	if err != nil {
		t.Fatalf("newNetConnection() error = %v", err)
	}
	return server, connection
}

func startLifecycleTestConnection(server *Server, connection *netConnection, transport *fakeLifecycleTransport) {
	server.connectionManager.addConnection(connection)
	connection.start(connection, func() {
		_, _ = transport.Read(make([]byte, 1))
	})
}

func TestConnectionConcurrentCloseRequestsCloseTransportOnce(t *testing.T) {
	transport := newFakeLifecycleTransport()
	server, connection := newLifecycleTestConnection(t, transport, 1)
	startLifecycleTestConnection(server, connection, transport)

	firstErr := errors.New("first close reason")
	connection.requestClose(OperationProtocol, firstErr)

	var callers sync.WaitGroup
	for i := 0; i < 50; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			connection.requestClose(OperationRead, errors.New("later close reason"))
		}()
	}
	callers.Wait()

	if err := connection.wait(context.Background()); err != nil {
		t.Fatalf("wait() error = %v", err)
	}
	if got := transport.closeCount.Load(); got != 1 {
		t.Fatalf("transport close count = %d, want 1", got)
	}
	if got := server.connectionManager.connectionsCount(); got != 0 {
		t.Fatalf("manager connection count = %d, want 0", got)
	}

	op, err := connection.closeReason()
	if op != OperationProtocol || !errors.Is(err, firstErr) {
		t.Fatalf("close reason = (%q, %v), want (%q, %v)", op, err, OperationProtocol, firstErr)
	}
	if got := connection.connectionState(); got != connectionClosed {
		t.Fatalf("connection state = %v, want closed", got)
	}
}

func TestConnectionSendAfterCloseReturnsConnectionClosed(t *testing.T) {
	transport := newFakeLifecycleTransport()
	server, connection := newLifecycleTestConnection(t, transport, 1)
	startLifecycleTestConnection(server, connection, transport)

	connection.requestClose(OperationRead, net.ErrClosed)
	if err := connection.Send(context.Background(), 1, []byte("body")); !errors.Is(err, ErrConnectionClosed) {
		t.Fatalf("Send() error = %v, want %v", err, ErrConnectionClosed)
	}
	if err := connection.wait(context.Background()); err != nil {
		t.Fatalf("wait() error = %v", err)
	}
}

func TestConnectionBlockedSendReleasedWhenSendsStop(t *testing.T) {
	writeGate := make(chan struct{})
	transport := newFakeLifecycleTransport()
	transport.writeGate = writeGate
	transport.writeStarted = make(chan struct{})
	server, connection := newLifecycleTestConnection(t, transport, 1)
	startLifecycleTestConnection(server, connection, transport)

	if err := connection.Send(context.Background(), 1, []byte("first")); err != nil {
		t.Fatalf("Send(first) error = %v", err)
	}
	waitForSignal(t, transport.writeStarted, "first write start")
	if err := connection.Send(context.Background(), 2, []byte("second")); err != nil {
		t.Fatalf("Send(second) error = %v", err)
	}

	blockedResult := make(chan error, 1)
	go func() {
		blockedResult <- connection.Send(context.Background(), 3, []byte("blocked"))
	}()
	assertNoErrorResult(t, blockedResult, "blocked send")

	drainResult := make(chan error, 1)
	go func() {
		drainResult <- connection.stopSendsAndDrain(context.Background())
	}()

	select {
	case err := <-blockedResult:
		if !errors.Is(err, ErrConnectionClosed) {
			t.Fatalf("blocked Send() error = %v, want %v", err, ErrConnectionClosed)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked Send() did not return when sends stopped")
	}

	close(writeGate)
	if err := <-drainResult; err != nil {
		t.Fatalf("stopSendsAndDrain() error = %v", err)
	}

	messages := transport.writtenMessages(t)
	if len(messages) != 2 || messages[0].Event != 1 || messages[1].Event != 2 {
		t.Fatalf("written events = %v, want [1 2]", messageEvents(messages))
	}

	connection.requestClose(OperationRead, net.ErrClosed)
	if err := connection.wait(context.Background()); err != nil {
		t.Fatalf("wait() error = %v", err)
	}
}

func TestConnectionSendDrainWritesAcceptedMessages(t *testing.T) {
	transport := newFakeLifecycleTransport()
	server, connection := newLifecycleTestConnection(t, transport, 4)
	startLifecycleTestConnection(server, connection, transport)

	for event := uint32(1); event <= 20; event++ {
		if err := connection.Send(context.Background(), event, []byte("body")); err != nil {
			t.Fatalf("Send(%d) error = %v", event, err)
		}
	}

	if err := connection.stopSendsAndDrain(context.Background()); err != nil {
		t.Fatalf("stopSendsAndDrain() error = %v", err)
	}

	messages := transport.writtenMessages(t)
	if len(messages) != 20 {
		t.Fatalf("written message count = %d, want 20", len(messages))
	}
	for i, message := range messages {
		if want := uint32(i + 1); message.Event != want {
			t.Fatalf("written event %d = %d, want %d", i, message.Event, want)
		}
	}

	connection.requestClose(OperationRead, net.ErrClosed)
	if err := connection.wait(context.Background()); err != nil {
		t.Fatalf("wait() error = %v", err)
	}
}

func TestConnectionChildTriggeredCloseDoesNotDeadlock(t *testing.T) {
	transport := newFakeLifecycleTransport()
	server, connection := newLifecycleTestConnection(t, transport, 1)
	childReturned := make(chan struct{})

	server.connectionManager.addConnection(connection)
	connection.start(connection, func() {
		connection.requestClose(OperationRead, net.ErrClosed)
		close(childReturned)
	})

	waitForSignal(t, childReturned, "child close return")
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := connection.wait(waitCtx); err != nil {
		t.Fatalf("wait() error = %v", err)
	}
}

func TestConnectionOpenAndCloseHooksRunOnce(t *testing.T) {
	transport := newFakeLifecycleTransport()
	server, connection := newLifecycleTestConnection(t, transport, 1)
	var opened atomic.Int32
	var closed atomic.Int32
	server.connectionOpen = func(Connection) { opened.Add(1) }
	server.connectionClose = func(Connection) { closed.Add(1) }

	startLifecycleTestConnection(server, connection, transport)
	connection.start(connection, func() {})

	var callers sync.WaitGroup
	for i := 0; i < 20; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			connection.requestClose(OperationRead, net.ErrClosed)
		}()
	}
	callers.Wait()

	if err := connection.wait(context.Background()); err != nil {
		t.Fatalf("wait() error = %v", err)
	}
	if got := opened.Load(); got != 1 {
		t.Fatalf("open hook count = %d, want 1", got)
	}
	if got := closed.Load(); got != 1 {
		t.Fatalf("close hook count = %d, want 1", got)
	}
}

func TestConnectionOpenHookCompletesBeforeCloseHook(t *testing.T) {
	transport := newFakeLifecycleTransport()
	server, connection := newLifecycleTestConnection(t, transport, 1)
	openStarted := make(chan struct{})
	openRelease := make(chan struct{})
	readerClosed := make(chan struct{})
	closeCalled := make(chan struct{})
	server.connectionOpen = func(Connection) {
		close(openStarted)
		<-openRelease
	}
	server.connectionClose = func(Connection) {
		close(closeCalled)
	}

	server.connectionManager.addConnection(connection)
	go connection.start(connection, func() {
		connection.requestClose(OperationRead, net.ErrClosed)
		close(readerClosed)
	})

	waitForSignal(t, openStarted, "open hook start")
	waitForSignal(t, readerClosed, "reader close request")
	assertNotClosed(t, closeCalled, "close hook before open hook completion")
	close(openRelease)

	if err := connection.wait(context.Background()); err != nil {
		t.Fatalf("wait() error = %v", err)
	}
	waitForSignal(t, closeCalled, "close hook after open hook completion")
}

func TestConnectionCloseRequestAfterGracefulFinalizationDoesNotRegressState(t *testing.T) {
	transport := newFakeLifecycleTransport()
	server, connection := newLifecycleTestConnection(t, transport, 1)
	startLifecycleTestConnection(server, connection, transport)

	if err := connection.quiesceReads(); err != nil {
		t.Fatalf("quiesceReads() error = %v", err)
	}
	if err := connection.stopSendsAndDrain(context.Background()); err != nil {
		t.Fatalf("stopSendsAndDrain() error = %v", err)
	}
	if err := connection.wait(context.Background()); err != nil {
		t.Fatalf("wait() error = %v", err)
	}

	connection.requestClose(OperationRead, net.ErrClosed)

	if got := connection.connectionState(); got != connectionClosed {
		t.Fatalf("connection state = %v, want closed", got)
	}
	if got := transport.closeCount.Load(); got != 1 {
		t.Fatalf("transport close count = %d, want 1", got)
	}
}

func TestConnectionRepeatedReadQuiesceSetsDeadlineOnce(t *testing.T) {
	transport := newFakeLifecycleTransport()
	_, connection := newLifecycleTestConnection(t, transport, 1)

	if err := connection.quiesceReads(); err != nil {
		t.Fatalf("first quiesceReads() error = %v", err)
	}
	if err := connection.quiesceReads(); err != nil {
		t.Fatalf("second quiesceReads() error = %v", err)
	}
	if got := transport.readDeadlineCount.Load(); got != 1 {
		t.Fatalf("read deadline count = %d, want 1", got)
	}
}

func messageEvents(messages []Message) []uint32 {
	events := make([]uint32, len(messages))
	for i, message := range messages {
		events[i] = message.Event
	}
	return events
}

func assertNoErrorResult(t *testing.T, result <-chan error, label string) {
	t.Helper()

	select {
	case err := <-result:
		t.Fatalf("%s returned early with %v", label, err)
	case <-time.After(20 * time.Millisecond):
	}
}
