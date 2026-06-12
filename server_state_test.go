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

type testLifecycleListener struct {
	closed     chan struct{}
	closeOnce  sync.Once
	closeCount atomic.Int32
}

type runtimeFailureListener struct {
	fail      chan struct{}
	closed    chan struct{}
	err       error
	closeOnce sync.Once
}

func newRuntimeFailureListener(err error) *runtimeFailureListener {
	return &runtimeFailureListener{fail: make(chan struct{}), closed: make(chan struct{}), err: err}
}

func (l *runtimeFailureListener) Accept() (net.Conn, error) {
	select {
	case <-l.fail:
		return nil, l.err
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *runtimeFailureListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}
func (l *runtimeFailureListener) Addr() net.Addr { return lifecycleTestAddr("runtime-listener") }

func newTestLifecycleListener() *testLifecycleListener {
	return &testLifecycleListener{closed: make(chan struct{})}
}

func (l *testLifecycleListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}
func (l *testLifecycleListener) Close() error {
	l.closeOnce.Do(func() {
		l.closeCount.Add(1)
		close(l.closed)
	})
	return nil
}
func (l *testLifecycleListener) Addr() net.Addr { return lifecycleTestAddr("listener") }

func waitForServerState(t *testing.T, server *Server, want serverState) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if server.currentState() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("server state = %v, want %v", server.currentState(), want)
}

func waitForServerRunResult(t *testing.T, runDone <-chan error) error {
	t.Helper()
	select {
	case err := <-runDone:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Run() result")
		return nil
	}
}

func newLifecycleServer(t *testing.T, transports ...Transport) *Server {
	t.Helper()
	server, err := NewServer(
		WithTransports(transports...),
		WithPort(0),
		WithWebSocketPort(0),
		WithShutdownTimeout(time.Second),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}

func TestServerStateRunClaimsNewAndRejectsConcurrentRun(t *testing.T) {
	server := newLifecycleServer(t, TransportTCP)
	listener := newTestLifecycleListener()
	server.tcpListen = func(string, string) (net.Listener, error) { return listener, nil }

	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(context.Background()) }()
	waitForServerState(t, server, stateRunning)

	if err := server.Run(context.Background()); !errors.Is(err, ErrServerRunning) {
		t.Fatalf("concurrent Run() error = %v, want %v", err, ErrServerRunning)
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := <-runDone; err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if err := server.Run(context.Background()); !errors.Is(err, ErrServerStopped) {
		t.Fatalf("Run() after stop error = %v, want %v", err, ErrServerStopped)
	}
}

func TestServerStateShutdownNewReturnsNil(t *testing.T) {
	server := newLifecycleServer(t, TransportTCP)
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown(new) error = %v", err)
	}
	if got := server.currentState(); got != stateNew {
		t.Fatalf("state after Shutdown(new) = %v, want new", got)
	}
}

func TestServerRunRevalidatesMutableConfiguration(t *testing.T) {
	server := newLifecycleServer(t, TransportTCP)
	server.WorkerCount = 0
	if err := server.Run(context.Background()); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("Run() error = %v, want %v", err, ErrInvalidConfiguration)
	}
	if got := server.currentState(); got != stateStopped {
		t.Fatalf("state = %v, want stopped", got)
	}
}

func TestServerRunRejectsDuplicateMutatedTransportsBeforeBinding(t *testing.T) {
	server := newLifecycleServer(t, TransportTCP)
	server.Transports = []Transport{TransportTCP, TransportTCP}
	var binds atomic.Int32
	server.tcpListen = func(string, string) (net.Listener, error) {
		binds.Add(1)
		return newTestLifecycleListener(), nil
	}

	if err := server.Run(context.Background()); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("Run() error = %v, want %v", err, ErrInvalidConfiguration)
	}
	if got := binds.Load(); got != 0 {
		t.Fatalf("listener bind count = %d, want 0", got)
	}
}

func TestRouterFreezeRejectsMutationWhileRunningAndAfterStop(t *testing.T) {
	server := newLifecycleServer(t, TransportTCP)
	listener := newTestLifecycleListener()
	server.tcpListen = func(string, string) (net.Listener, error) { return listener, nil }
	if err := server.Use(func(*Context) {}); err != nil {
		t.Fatalf("Use(new) error = %v", err)
	}
	if err := server.RegisterRoute(1, func(*Context) {}); err != nil {
		t.Fatalf("RegisterRoute(new) error = %v", err)
	}

	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(context.Background()) }()
	waitForServerState(t, server, stateRunning)
	if err := server.Use(func(*Context) {}); !errors.Is(err, ErrServerRunning) {
		t.Fatalf("Use(running) error = %v, want %v", err, ErrServerRunning)
	}
	if err := server.RegisterRoute(2, func(*Context) {}); !errors.Is(err, ErrServerRunning) {
		t.Fatalf("RegisterRoute(running) error = %v, want %v", err, ErrServerRunning)
	}
	if err := server.OnConnectionOpen(func(Connection) {}); !errors.Is(err, ErrServerRunning) {
		t.Fatalf("OnConnectionOpen(running) error = %v, want %v", err, ErrServerRunning)
	}

	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := <-runDone; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if err := server.Use(func(*Context) {}); !errors.Is(err, ErrServerStopped) {
		t.Fatalf("Use(stopped) error = %v, want %v", err, ErrServerStopped)
	}
}

func TestRouterFreezeRejectsMutationDuringStartingAndCopiesHandlers(t *testing.T) {
	server := newLifecycleServer(t, TransportTCP)
	bindEntered := make(chan struct{})
	releaseBind := make(chan struct{})
	listener := newTestLifecycleListener()
	server.tcpListen = func(string, string) (net.Listener, error) {
		close(bindEntered)
		<-releaseBind
		return listener, nil
	}
	first := func(*Context) {}
	if err := server.RegisterRoute(7, first); err != nil {
		t.Fatalf("RegisterRoute(new) error = %v", err)
	}

	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(context.Background()) }()
	waitForSignal(t, bindEntered, "startup bind")
	if err := server.Use(func(*Context) {}); !errors.Is(err, ErrServerRunning) {
		t.Fatalf("Use(starting) error = %v, want %v", err, ErrServerRunning)
	}
	if err := server.RegisterRoute(8, func(*Context) {}); !errors.Is(err, ErrServerRunning) {
		t.Fatalf("RegisterRoute(starting) error = %v, want %v", err, ErrServerRunning)
	}
	close(releaseBind)
	waitForServerState(t, server, stateRunning)

	server.router.mu.Lock()
	server.router.routes[7][0] = func(*Context) { t.Fatal("mutated route snapshot executed") }
	server.router.mu.Unlock()
	if len(server.runtimeRoutes[7]) != 1 {
		t.Fatalf("runtime route handler count = %d, want 1", len(server.runtimeRoutes[7]))
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := <-runDone; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestServerStartupCancellationRollsBackListener(t *testing.T) {
	server := newLifecycleServer(t, TransportTCP, TransportWebSocket)
	listener := newTestLifecycleListener()
	ctx, cancel := context.WithCancel(context.Background())
	server.tcpListen = func(string, string) (net.Listener, error) {
		cancel()
		return listener, nil
	}
	server.webSocketListen = func(string, string) (net.Listener, error) {
		t.Fatal("websocket listener should not be created after cancellation")
		return nil, nil
	}

	if err := server.Run(ctx); err != nil {
		t.Fatalf("Run(canceled during startup) error = %v, want nil", err)
	}
	if got := listener.closeCount.Load(); got != 1 {
		t.Fatalf("listener close count = %d, want 1", got)
	}
	if got := server.currentState(); got != stateStopped {
		t.Fatalf("state = %v, want stopped", got)
	}
}

func TestServerStartupBindFailureRollsBackPriorListener(t *testing.T) {
	server := newLifecycleServer(t, TransportTCP, TransportWebSocket)
	listener := newTestLifecycleListener()
	bindErr := errors.New("websocket bind failed")
	server.tcpListen = func(string, string) (net.Listener, error) { return listener, nil }
	server.webSocketListen = func(string, string) (net.Listener, error) { return nil, bindErr }

	if err := server.Run(context.Background()); !errors.Is(err, bindErr) {
		t.Fatalf("Run() error = %v, want %v", err, bindErr)
	}
	if got := listener.closeCount.Load(); got != 1 {
		t.Fatalf("prior listener close count = %d, want 1", got)
	}
	if got := server.currentState(); got != stateStopped {
		t.Fatalf("state = %v, want stopped", got)
	}
}

func TestServerStartupInvalidTLSRollsBackAllBoundListeners(t *testing.T) {
	server, err := NewServer(
		WithTransports(TransportTCP, TransportWebSocket),
		WithPort(0),
		WithWebSocketPort(0),
		WithCertFile("missing-cert.pem"),
		WithPrivateKeyFile("missing-key.pem"),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	first := newTestLifecycleListener()
	second := newTestLifecycleListener()
	server.tcpListen = func(string, string) (net.Listener, error) { return first, nil }
	server.webSocketListen = func(string, string) (net.Listener, error) { return second, nil }

	if err := server.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil, want TLS certificate error")
	}
	if first.closeCount.Load() != 1 || second.closeCount.Load() != 1 {
		t.Fatalf("listener close counts = (%d, %d), want (1, 1)", first.closeCount.Load(), second.closeCount.Load())
	}
}

func TestServerStartupRejectsMalformedWebSocketPatternBeforeBinding(t *testing.T) {
	server, err := NewServer(WithTransports(TransportWebSocket), WithWebSocketPort(0))
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	server.WebSocketPath = "/{identifier"
	var binds atomic.Int32
	server.webSocketListen = func(string, string) (net.Listener, error) {
		binds.Add(1)
		return newTestLifecycleListener(), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := server.Run(ctx); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("Run() error = %v, want %v", err, ErrInvalidConfiguration)
	}
	if got := binds.Load(); got != 0 {
		t.Fatalf("listener bind count = %d, want 0", got)
	}
	if got := server.currentState(); got != stateStopped {
		t.Fatalf("state = %v, want stopped", got)
	}
}

func TestServerStartupFailureReleasesWaitingShutdownWithoutStoringCause(t *testing.T) {
	server := newLifecycleServer(t, TransportTCP, TransportWebSocket)
	first := newTestLifecycleListener()
	bindEntered := make(chan struct{})
	releaseBind := make(chan struct{})
	bindErr := errors.New("startup failed")
	server.tcpListen = func(string, string) (net.Listener, error) { return first, nil }
	server.webSocketListen = func(string, string) (net.Listener, error) {
		close(bindEntered)
		<-releaseBind
		return nil, bindErr
	}

	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(context.Background()) }()
	waitForSignal(t, bindEntered, "second bind entry")
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- server.Shutdown(context.Background()) }()
	close(releaseBind)

	if err := <-runDone; !errors.Is(err, bindErr) {
		t.Fatalf("Run() error = %v, want %v", err, bindErr)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("waiting Shutdown() error = %v, want nil", err)
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("later Shutdown() error = %v, want nil", err)
	}
}

func TestServerShutdownDuringStartingRequestsRollbackAndWaits(t *testing.T) {
	server := newLifecycleServer(t, TransportTCP)
	bindEntered := make(chan struct{})
	releaseBind := make(chan struct{})
	listener := newTestLifecycleListener()
	server.tcpListen = func(string, string) (net.Listener, error) {
		close(bindEntered)
		<-releaseBind
		return listener, nil
	}

	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(context.Background()) }()
	waitForSignal(t, bindEntered, "startup bind")
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- server.Shutdown(context.Background()) }()

	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown() returned before startup rollback: %v", err)
	default:
	}
	close(releaseBind)
	if err := waitForServerRunResult(t, runDone); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Shutdown() after startup rollback")
	}
	if got := listener.closeCount.Load(); got != 1 {
		t.Fatalf("listener close count = %d, want 1", got)
	}
	if got := server.currentState(); got != stateStopped {
		t.Fatalf("server state = %v, want stopped", got)
	}
}

func TestHookAndErrorHandlerPanicsAreRecovered(t *testing.T) {
	server := newLifecycleServer(t, TransportTCP)
	connection := newManagedConnectionStub(1)
	var reports atomic.Int32
	server.connectionError = func(Connection, ConnectionOperation, error) {
		reports.Add(1)
	}
	server.connectionOpen = func(Connection) { panic("open panic") }
	server.invokeOpenHook(connection)
	if connection.closeCount.Load() != 1 {
		t.Fatalf("open-hook panic close count = %d, want 1", connection.closeCount.Load())
	}

	server.connectionClose = func(Connection) { panic("close panic") }
	server.invokeCloseHook(connection)
	if reports.Load() != 2 {
		t.Fatalf("hook panic report count = %d, want 2", reports.Load())
	}

	server.connectionError = func(Connection, ConnectionOperation, error) { panic("error handler panic") }
	server.reportConnectionError(connection, OperationRead, errors.New("read failed"))
}

func TestServerShutdownTimeoutForNonCooperativeHandler(t *testing.T) {
	server, err := NewServer(
		WithTransports(TransportTCP),
		WithPort(0),
		WithShutdownTimeout(30*time.Millisecond),
		WithWorkerCount(1),
		WithWorkerQueueCapacity(1),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	listener := newTestLifecycleListener()
	server.tcpListen = func(string, string) (net.Listener, error) { return listener, nil }
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	if err := server.RegisterRoute(9, func(*Context) {
		close(started)
		<-release
	}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}

	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(context.Background()) }()
	waitForServerState(t, server, stateRunning)
	if err := server.handleRequest(&testConnection{id: 1}, newRequest(Message{Event: 9})); err != nil {
		t.Fatalf("handleRequest() error = %v", err)
	}
	waitForSignal(t, started, "non-cooperative handler")
	if err := server.Shutdown(context.Background()); !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("Shutdown() error = %v, want %v", err, ErrShutdownTimeout)
	}
	if err := <-runDone; !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("Run() error = %v, want %v", err, ErrShutdownTimeout)
	}
	releaseOnce.Do(func() { close(release) })
}

func TestServerShutdownWaitsForAcceptedConnectionSetup(t *testing.T) {
	server := newLifecycleServer(t, TransportTCP)
	listener := newTestLifecycleListener()
	server.tcpListen = func(string, string) (net.Listener, error) { return listener, nil }
	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(context.Background()) }()
	waitForServerState(t, server, stateRunning)
	if !server.beginConnectionSetup() {
		t.Fatal("beginConnectionSetup() = false, want true while running")
	}

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- server.Shutdown(context.Background()) }()
	waitForServerState(t, server, stateStopping)
	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown() returned before setup completed: %v", err)
	default:
	}
	server.finishConnectionSetup()

	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for shutdown after setup completion")
	}
	if err := <-runDone; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestServerShutdownConcurrentCallersShareCompletion(t *testing.T) {
	server := newLifecycleServer(t, TransportTCP)
	listener := newTestLifecycleListener()
	server.tcpListen = func(string, string) (net.Listener, error) { return listener, nil }
	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(context.Background()) }()
	waitForServerState(t, server, stateRunning)
	if !server.beginConnectionSetup() {
		t.Fatal("beginConnectionSetup() = false while running")
	}

	const callers = 12
	results := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() { results <- server.Shutdown(context.Background()) }()
	}
	waitForServerState(t, server, stateStopping)
	server.finishConnectionSetup()
	for i := 0; i < callers; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("concurrent Shutdown() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for concurrent Shutdown() caller")
		}
	}
	if err := waitForServerRunResult(t, runDone); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("later Shutdown() error = %v", err)
	}
}

func TestServerShutdownCallerTimeoutDoesNotCancelSharedCleanup(t *testing.T) {
	server := newLifecycleServer(t, TransportTCP)
	listener := newTestLifecycleListener()
	server.tcpListen = func(string, string) (net.Listener, error) { return listener, nil }
	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(context.Background()) }()
	waitForServerState(t, server, stateRunning)
	if !server.beginConnectionSetup() {
		t.Fatal("beginConnectionSetup() = false while running")
	}

	shortCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := server.Shutdown(shortCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("short Shutdown() error = %v, want %v", err, context.DeadlineExceeded)
	}
	server.finishConnectionSetup()

	waitCtx, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	if err := server.Shutdown(waitCtx); err != nil {
		t.Fatalf("waiting Shutdown() error = %v", err)
	}
	if err := waitForServerRunResult(t, runDone); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestServerRunContextCancellationSharesShutdownSequence(t *testing.T) {
	server := newLifecycleServer(t, TransportTCP)
	listener := newTestLifecycleListener()
	server.tcpListen = func(string, string) (net.Listener, error) { return listener, nil }
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(runCtx) }()
	waitForServerState(t, server, stateRunning)
	if !server.beginConnectionSetup() {
		t.Fatal("beginConnectionSetup() = false while running")
	}

	cancelRun()
	waitForServerState(t, server, stateStopping)
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- server.Shutdown(context.Background()) }()
	server.finishConnectionSetup()

	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Shutdown()")
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run(canceled) error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Run()")
	}
}

func TestServerShutdownTimeoutIsStableForLaterCallers(t *testing.T) {
	server, err := NewServer(
		WithTransports(TransportTCP),
		WithPort(0),
		WithShutdownTimeout(30*time.Millisecond),
		WithWorkerCount(1),
		WithWorkerQueueCapacity(1),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	listener := newTestLifecycleListener()
	server.tcpListen = func(string, string) (net.Listener, error) { return listener, nil }
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	if err := server.RegisterRoute(11, func(*Context) {
		close(started)
		<-release
	}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(context.Background()) }()
	waitForServerState(t, server, stateRunning)
	if err := server.handleRequest(&testConnection{id: 1}, newRequest(Message{Event: 11})); err != nil {
		t.Fatalf("handleRequest() error = %v", err)
	}
	waitForSignal(t, started, "non-cooperative handler")

	if err := server.Shutdown(context.Background()); !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("Shutdown() error = %v, want %v", err, ErrShutdownTimeout)
	}
	if err := waitForServerRunResult(t, runDone); !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("Run() error = %v, want %v", err, ErrShutdownTimeout)
	}
	if err := server.Shutdown(context.Background()); !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("later Shutdown() error = %v, want %v", err, ErrShutdownTimeout)
	}
	releaseOnce.Do(func() { close(release) })
}

func TestServerShutdownForceCancellationReachesCooperativeHandler(t *testing.T) {
	server, err := NewServer(
		WithTransports(TransportTCP),
		WithPort(0),
		WithShutdownTimeout(30*time.Millisecond),
		WithWorkerCount(1),
		WithWorkerQueueCapacity(1),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	listener := newTestLifecycleListener()
	server.tcpListen = func(string, string) (net.Listener, error) { return listener, nil }
	started := make(chan struct{})
	canceled := make(chan struct{})
	if err := server.RegisterRoute(12, func(ctx *Context) {
		close(started)
		<-ctx.Done()
		close(canceled)
	}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(context.Background()) }()
	waitForServerState(t, server, stateRunning)
	if err := server.handleRequest(&testConnection{id: 1}, newRequest(Message{Event: 12})); err != nil {
		t.Fatalf("handleRequest() error = %v", err)
	}
	waitForSignal(t, started, "cooperative handler")

	if err := server.Shutdown(context.Background()); !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("Shutdown() error = %v, want %v", err, ErrShutdownTimeout)
	}
	waitForSignal(t, canceled, "cooperative handler cancellation")
	if err := waitForServerRunResult(t, runDone); !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("Run() error = %v, want %v", err, ErrShutdownTimeout)
	}
}

func TestServerRunWrapsUnexpectedServingError(t *testing.T) {
	server := newLifecycleServer(t, TransportTCP)
	runtimeErr := errors.New("accept failed")
	listener := newRuntimeFailureListener(runtimeErr)
	server.tcpListen = func(string, string) (net.Listener, error) { return listener, nil }

	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(context.Background()) }()
	waitForServerState(t, server, stateRunning)
	close(listener.fail)

	select {
	case err := <-runDone:
		if !errors.Is(err, runtimeErr) {
			t.Fatalf("Run() error = %v, want wrapped %v", err, runtimeErr)
		}
		if err == runtimeErr {
			t.Fatal("Run() returned runtime error directly, want wrapped error")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runtime failure shutdown")
	}
}

func TestServerRunWrapsUnexpectedWebSocketServingError(t *testing.T) {
	server := newLifecycleServer(t, TransportWebSocket)
	runtimeErr := errors.New("websocket accept failed")
	listener := newRuntimeFailureListener(runtimeErr)
	server.webSocketListen = func(string, string) (net.Listener, error) { return listener, nil }

	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(context.Background()) }()
	waitForServerState(t, server, stateRunning)
	close(listener.fail)

	select {
	case err := <-runDone:
		if !errors.Is(err, runtimeErr) {
			t.Fatalf("Run() error = %v, want wrapped %v", err, runtimeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for WebSocket runtime failure shutdown")
	}
}

func TestServerRunJoinsServingErrorAndShutdownTimeout(t *testing.T) {
	server, err := NewServer(
		WithTransports(TransportTCP),
		WithPort(0),
		WithShutdownTimeout(30*time.Millisecond),
		WithWorkerCount(1),
		WithWorkerQueueCapacity(1),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	runtimeErr := errors.New("accept failed")
	listener := newRuntimeFailureListener(runtimeErr)
	server.tcpListen = func(string, string) (net.Listener, error) { return listener, nil }
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	if err := server.RegisterRoute(10, func(*Context) {
		close(started)
		<-release
	}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}

	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(context.Background()) }()
	waitForServerState(t, server, stateRunning)
	if err := server.handleRequest(&testConnection{id: 1}, newRequest(Message{Event: 10})); err != nil {
		t.Fatalf("handleRequest() error = %v", err)
	}
	waitForSignal(t, started, "non-cooperative runtime handler")
	close(listener.fail)

	select {
	case err := <-runDone:
		if !errors.Is(err, runtimeErr) || !errors.Is(err, ErrShutdownTimeout) {
			t.Fatalf("Run() error = %v, want both runtime cause and %v", err, ErrShutdownTimeout)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for joined runtime/shutdown error")
	}
	releaseOnce.Do(func() { close(release) })
}
