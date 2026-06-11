package ramix

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type tcpTestError struct {
	op  ConnectionOperation
	err error
}

type tcpTestDecoderFunc func([]byte) (Message, error)

func (f tcpTestDecoderFunc) Decode(data []byte) (Message, error) {
	return f(data)
}

func newTCPTestServer(t *testing.T, options ...ServerOption) *Server {
	t.Helper()
	options = append(options,
		WithWorkerCount(1),
		WithWorkerQueueCapacity(8),
		WithHeartbeatInterval(time.Hour),
		WithHeartbeatTimeout(2*time.Hour),
	)
	server, err := NewServer(options...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	server.workerPool.start()
	t.Cleanup(func() {
		_ = server.workerPool.stopAcceptingAndDrain(context.Background())
	})
	return server
}

func openTCPTestConnection(t *testing.T, server *Server) (*TCPConnection, net.Conn) {
	t.Helper()
	serverSide, clientSide := net.Pipe()
	connectionID := atomic.AddUint64(&server.currentConnectionID, 1)
	base, err := newNetConnection(connectionID, server, serverSide, func(data []byte) error {
		return writeFull(serverSide, data)
	})
	if err != nil {
		t.Fatalf("newNetConnection() error = %v", err)
	}
	connection := &TCPConnection{netConnection: base, socket: serverSide}
	server.connectionManager.addConnection(connection)
	connection.open()
	t.Cleanup(func() {
		_ = clientSide.Close()
		connection.requestClose(OperationRead, net.ErrClosed)
		waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := connection.wait(waitCtx); err != nil {
			t.Errorf("connection cleanup wait error = %v", err)
		}
	})
	return connection, clientSide
}

func encodeTCPTestMessage(t *testing.T, event uint32, body string) []byte {
	t.Helper()
	data, err := (&Encoder{}).Encode(Message{Event: event, Body: []byte(body)})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	return data
}

func TestTCPConnectionSplitFrameRoutesExactlyOnce(t *testing.T) {
	server := newTCPTestServer(t)
	routed := make(chan string, 2)
	server.RegisterRoute(7, func(ctx *Context) {
		routed <- string(ctx.Request.Message.Body)
	})
	_, client := openTCPTestConnection(t, server)

	frame := encodeTCPTestMessage(t, 7, "split")
	if _, err := client.Write(frame[:5]); err != nil {
		t.Fatalf("Write(first) error = %v", err)
	}
	assertNoStringResult(t, routed, "route before complete frame")
	if _, err := client.Write(frame[5:]); err != nil {
		t.Fatalf("Write(second) error = %v", err)
	}

	if got := waitForString(t, routed, "split frame route"); got != "split" {
		t.Fatalf("routed body = %q, want split", got)
	}
	assertNoStringResult(t, routed, "duplicate split frame route")
}

func TestTCPConnectionCoalescedFramesRouteInOrder(t *testing.T) {
	server := newTCPTestServer(t)
	routed := make(chan string, 2)
	server.RegisterRoute(8, func(ctx *Context) {
		routed <- string(ctx.Request.Message.Body)
	})
	_, client := openTCPTestConnection(t, server)

	first := encodeTCPTestMessage(t, 8, "first")
	second := encodeTCPTestMessage(t, 8, "second")
	if _, err := client.Write(append(first, second...)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if got := waitForString(t, routed, "first coalesced route"); got != "first" {
		t.Fatalf("first routed body = %q", got)
	}
	if got := waitForString(t, routed, "second coalesced route"); got != "second" {
		t.Fatalf("second routed body = %q", got)
	}
}

func TestTCPConnectionProtocolFailuresAreReportedAndCloseConnection(t *testing.T) {
	tests := []struct {
		name    string
		options []ServerOption
		payload func(*testing.T) []byte
		wantErr error
		decoder DecoderInterface
	}{
		{
			name: "truncated",
			payload: func(t *testing.T) []byte {
				return encodeTCPTestMessage(t, 1, "body")[:6]
			},
			wantErr: ErrInvalidFrame,
		},
		{
			name:    "oversized",
			options: []ServerOption{WithServerMaxFrameLength(12)},
			payload: func(t *testing.T) []byte {
				return encodeTCPTestMessage(t, 1, "too-large")
			},
			wantErr: ErrFrameTooLarge,
		},
		{
			name: "mismatched message body",
			payload: func(t *testing.T) []byte {
				return encodeTCPTestMessage(t, 1, "body")
			},
			wantErr: ErrInvalidFrame,
			decoder: tcpTestDecoderFunc(func([]byte) (Message, error) {
				return Message{}, errors.Join(ErrInvalidFrame, errors.New("declared body length mismatch"))
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newTCPTestServer(t, tt.options...)
			if tt.decoder != nil {
				server.decoder = tt.decoder
			}
			reported := make(chan tcpTestError, 1)
			server.connectionError = func(_ Connection, op ConnectionOperation, err error) {
				reported <- tcpTestError{op: op, err: err}
			}
			connection, client := openTCPTestConnection(t, server)

			if _, err := client.Write(tt.payload(t)); err != nil {
				t.Fatalf("Write() error = %v", err)
			}
			_ = client.Close()

			report := waitForTCPError(t, reported, "protocol error")
			if report.op != OperationProtocol || !errors.Is(report.err, tt.wantErr) {
				t.Fatalf("reported error = (%q, %v), want (%q, %v)", report.op, report.err, OperationProtocol, tt.wantErr)
			}
			if err := connection.wait(context.Background()); err != nil {
				t.Fatalf("wait() error = %v", err)
			}
		})
	}
}

type shortWriter struct {
	max      int
	zero     bool
	invalid  int
	err      error
	mu       sync.Mutex
	written  []byte
	writeOps int
}

func (w *shortWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writeOps++
	if w.zero {
		return 0, nil
	}
	if w.invalid != 0 {
		return w.invalid, w.err
	}
	n := len(data)
	if n > w.max {
		n = w.max
	}
	w.written = append(w.written, data[:n]...)
	return n, nil
}

func TestTCPConnectionWriteFullRetriesShortWrites(t *testing.T) {
	writer := &shortWriter{max: 2}
	data := []byte("abcdef")
	if err := writeFull(writer, data); err != nil {
		t.Fatalf("writeFull() error = %v", err)
	}
	if got := string(writer.written); got != string(data) {
		t.Fatalf("written data = %q, want %q", got, data)
	}
	if writer.writeOps < 3 {
		t.Fatalf("write operations = %d, want at least 3", writer.writeOps)
	}
}

func TestTCPConnectionWriteFullRejectsZeroProgress(t *testing.T) {
	err := writeFull(&shortWriter{zero: true}, []byte("data"))
	if !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("writeFull() error = %v, want %v", err, io.ErrNoProgress)
	}
}

func TestTCPConnectionWriteFullRejectsInvalidCounts(t *testing.T) {
	for _, count := range []int{-1, 5} {
		err := writeFull(&shortWriter{invalid: count, err: errors.New("writer error")}, []byte("data"))
		if !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("writeFull() with count %d error = %v, want %v", count, err, io.ErrShortWrite)
		}
	}
}

func TestTCPConnectionWriteFailureReportsOperationWrite(t *testing.T) {
	server := newTCPTestServer(t)
	reported := make(chan tcpTestError, 1)
	server.connectionError = func(_ Connection, op ConnectionOperation, err error) {
		reported <- tcpTestError{op: op, err: err}
	}
	transport := newFakeLifecycleTransport()
	writeErr := errors.New("write failed")
	connection, err := newNetConnection(1, server, transport, func([]byte) error {
		return writeErr
	})
	if err != nil {
		t.Fatalf("newNetConnection() error = %v", err)
	}
	server.connectionManager.addConnection(connection)
	connection.start(connection, func() { <-connection.forceCtx.Done() })

	if err := connection.Send(context.Background(), 1, []byte("body")); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	report := waitForTCPError(t, reported, "write error")
	if report.op != OperationWrite || !errors.Is(report.err, writeErr) {
		t.Fatalf("reported error = (%q, %v), want (%q, %v)", report.op, report.err, OperationWrite, writeErr)
	}
	if err := connection.wait(context.Background()); err != nil {
		t.Fatalf("wait() error = %v", err)
	}
}

type quiesceTCPConn struct {
	deadlineSet chan struct{}
	readDone    chan struct{}
	closeCount  atomic.Int32
	deadline    sync.Once
}

type singleReadTCPConn struct {
	data      []byte
	err       error
	readOnce  sync.Once
	closed    chan struct{}
	closeOnce sync.Once
}

func newSingleReadTCPConn(data []byte, err error) *singleReadTCPConn {
	return &singleReadTCPConn{data: data, err: err, closed: make(chan struct{})}
}

func (c *singleReadTCPConn) Read(buffer []byte) (int, error) {
	read := false
	c.readOnce.Do(func() {
		read = true
	})
	if read {
		return copy(buffer, c.data), c.err
	}
	<-c.closed
	return 0, net.ErrClosed
}
func (c *singleReadTCPConn) Write(data []byte) (int, error) { return len(data), nil }
func (c *singleReadTCPConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}
func (c *singleReadTCPConn) LocalAddr() net.Addr              { return lifecycleTestAddr("local") }
func (c *singleReadTCPConn) RemoteAddr() net.Addr             { return lifecycleTestAddr("remote") }
func (c *singleReadTCPConn) SetDeadline(time.Time) error      { return nil }
func (c *singleReadTCPConn) SetReadDeadline(time.Time) error  { return nil }
func (c *singleReadTCPConn) SetWriteDeadline(time.Time) error { return nil }

func TestTCPConnectionReadDataWithUnexpectedErrorReportsRead(t *testing.T) {
	server := newTCPTestServer(t)
	server.RegisterRoute(12, func(*Context) {})
	reported := make(chan tcpTestError, 1)
	server.connectionError = func(_ Connection, op ConnectionOperation, err error) {
		reported <- tcpTestError{op: op, err: err}
	}
	resetErr := errors.New("connection reset")
	socket := newSingleReadTCPConn(encodeTCPTestMessage(t, 12, "body"), resetErr)
	base, err := newNetConnection(1, server, socket, func(data []byte) error { return writeFull(socket, data) })
	if err != nil {
		t.Fatalf("newNetConnection() error = %v", err)
	}
	connection := &TCPConnection{netConnection: base, socket: socket}
	server.connectionManager.addConnection(connection)
	connection.open()

	report := waitForTCPError(t, reported, "read error after data")
	if report.op != OperationRead || !errors.Is(report.err, resetErr) {
		t.Fatalf("reported error = (%q, %v), want (%q, %v)", report.op, report.err, OperationRead, resetErr)
	}
	if err := connection.wait(context.Background()); err != nil {
		t.Fatalf("wait() error = %v", err)
	}
}

func TestTCPConnectionConcurrentCloseSuppressesExpectedWriterError(t *testing.T) {
	server := newTCPTestServer(t)
	reported := make(chan tcpTestError, 1)
	server.connectionError = func(_ Connection, op ConnectionOperation, err error) {
		reported <- tcpTestError{op: op, err: err}
	}
	transport := newFakeLifecycleTransport()
	writeStarted := make(chan struct{})
	connection, err := newNetConnection(1, server, transport, func([]byte) error {
		close(writeStarted)
		<-transport.closed
		return net.ErrClosed
	})
	if err != nil {
		t.Fatalf("newNetConnection() error = %v", err)
	}
	server.connectionManager.addConnection(connection)
	connection.start(connection, func() { <-connection.forceCtx.Done() })
	if err := connection.Send(context.Background(), 1, []byte("body")); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	waitForSignal(t, writeStarted, "blocked write start")
	connection.requestClose(OperationProtocol, ErrInvalidFrame)
	if err := connection.wait(context.Background()); err != nil {
		t.Fatalf("wait() error = %v", err)
	}
	select {
	case report := <-reported:
		t.Fatalf("unexpected duplicate error report = (%q, %v)", report.op, report.err)
	default:
	}
}

func newQuiesceTCPConn() *quiesceTCPConn {
	return &quiesceTCPConn{deadlineSet: make(chan struct{}), readDone: make(chan struct{})}
}

func (c *quiesceTCPConn) Read([]byte) (int, error) {
	<-c.deadlineSet
	close(c.readDone)
	return 0, context.DeadlineExceeded
}
func (c *quiesceTCPConn) Write(data []byte) (int, error) { return len(data), nil }
func (c *quiesceTCPConn) Close() error {
	c.closeCount.Add(1)
	c.deadline.Do(func() { close(c.deadlineSet) })
	return nil
}
func (c *quiesceTCPConn) LocalAddr() net.Addr              { return lifecycleTestAddr("local") }
func (c *quiesceTCPConn) RemoteAddr() net.Addr             { return lifecycleTestAddr("remote") }
func (c *quiesceTCPConn) SetDeadline(time.Time) error      { return nil }
func (c *quiesceTCPConn) SetWriteDeadline(time.Time) error { return nil }
func (c *quiesceTCPConn) SetReadDeadline(time.Time) error {
	c.deadline.Do(func() { close(c.deadlineSet) })
	return nil
}

func TestTCPConnectionQuiesceStopsReaderWithoutClosingWriter(t *testing.T) {
	server := newTCPTestServer(t)
	socket := newQuiesceTCPConn()
	base, err := newNetConnection(1, server, socket, func(data []byte) error {
		return writeFull(socket, data)
	})
	if err != nil {
		t.Fatalf("newNetConnection() error = %v", err)
	}
	connection := &TCPConnection{netConnection: base, socket: socket}
	server.connectionManager.addConnection(connection)
	connection.open()

	if err := connection.quiesceReads(); err != nil {
		t.Fatalf("quiesceReads() error = %v", err)
	}
	waitForSignal(t, socket.readDone, "quiesced TCP reader")
	if got := socket.closeCount.Load(); got != 0 {
		t.Fatalf("transport close count = %d, want 0 before send drain", got)
	}
	if err := connection.Send(context.Background(), 9, []byte("response")); err != nil {
		t.Fatalf("Send() during draining error = %v", err)
	}
	if err := connection.stopSendsAndDrain(context.Background()); err != nil {
		t.Fatalf("stopSendsAndDrain() error = %v", err)
	}
	if err := connection.wait(context.Background()); err != nil {
		t.Fatalf("wait() error = %v", err)
	}
}

func TestTCPConnectionWorkerQueueFullReportsTaskAndClosesOnlyOffender(t *testing.T) {
	server, err := NewServer(
		WithWorkerCount(1),
		WithWorkerQueueCapacity(1),
		WithHeartbeatInterval(time.Hour),
		WithHeartbeatTimeout(2*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	server.workerPool.start()
	t.Cleanup(func() {
		drainCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.workerPool.stopAcceptingAndDrain(drainCtx); err != nil {
			t.Errorf("worker pool cleanup error = %v", err)
		}
	})

	handlerStarted := make(chan struct{})
	handlerRelease := make(chan struct{})
	var handlerReleaseOnce sync.Once
	t.Cleanup(func() {
		handlerReleaseOnce.Do(func() { close(handlerRelease) })
	})
	server.RegisterRoute(10, func(*Context) {
		select {
		case <-handlerStarted:
		default:
			close(handlerStarted)
		}
		<-handlerRelease
	})
	reported := make(chan tcpTestError, 1)
	server.connectionError = func(_ Connection, op ConnectionOperation, err error) {
		reported <- tcpTestError{op: op, err: err}
	}
	offender, offenderClient := openTCPTestConnection(t, server)
	healthy, healthyClient := openTCPTestConnection(t, server)

	first := encodeTCPTestMessage(t, 10, "first")
	if _, err := offenderClient.Write(first); err != nil {
		t.Fatalf("Write(first) error = %v", err)
	}
	waitForSignal(t, handlerStarted, "blocking handler start")
	second := encodeTCPTestMessage(t, 10, "second")
	third := encodeTCPTestMessage(t, 10, "third")
	if _, err := offenderClient.Write(append(second, third...)); err != nil {
		t.Fatalf("Write(second+third) error = %v", err)
	}

	report := waitForTCPError(t, reported, "worker queue full")
	if report.op != OperationTask || !errors.Is(report.err, ErrWorkerQueueFull) {
		t.Fatalf("reported error = (%q, %v), want (%q, %v)", report.op, report.err, OperationTask, ErrWorkerQueueFull)
	}
	if healthy.connectionState() != connectionOpen {
		t.Fatalf("healthy connection state = %v, want open", healthy.connectionState())
	}
	if offender.connectionState() < connectionClosing {
		t.Fatalf("offender connection state = %v, want closing/closed", offender.connectionState())
	}

	handlerReleaseOnce.Do(func() { close(handlerRelease) })
	_ = healthyClient.Close()
}

func TestTCPConnectionServerStoppingExitsWithoutErrorReport(t *testing.T) {
	server := newTCPTestServer(t)
	reported := make(chan tcpTestError, 1)
	server.connectionError = func(_ Connection, op ConnectionOperation, err error) {
		reported <- tcpTestError{op: op, err: err}
	}
	if err := server.workerPool.stopAcceptingAndDrain(context.Background()); err != nil {
		t.Fatalf("stopAcceptingAndDrain() error = %v", err)
	}
	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	base, err := newNetConnection(1, server, serverSide, func(data []byte) error { return writeFull(serverSide, data) })
	if err != nil {
		t.Fatalf("newNetConnection() error = %v", err)
	}
	connection := &TCPConnection{netConnection: base, socket: serverSide}
	readerDone := make(chan struct{})
	go func() {
		connection.reader()
		close(readerDone)
	}()
	if _, err := clientSide.Write(encodeTCPTestMessage(t, 11, "stopping")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	waitForSignal(t, readerDone, "TCP reader stopping exit")
	select {
	case report := <-reported:
		t.Fatalf("unexpected error report = (%q, %v)", report.op, report.err)
	default:
	}
	if got := connection.connectionState(); got != connectionOpen {
		t.Fatalf("connection state = %v, want open for shutdown owner", got)
	}
}

func waitForString(t *testing.T, ch <-chan string, label string) string {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return ""
	}
}

func assertNoStringResult(t *testing.T, ch <-chan string, label string) {
	t.Helper()
	select {
	case value := <-ch:
		t.Fatalf("%s unexpectedly returned %q", label, value)
	case <-time.After(20 * time.Millisecond):
	}
}

func waitForTCPError(t *testing.T, ch <-chan tcpTestError, label string) tcpTestError {
	t.Helper()
	select {
	case report := <-ch:
		return report
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return tcpTestError{}
	}
}
