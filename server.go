package ramix

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

type listenFunc func(network, address string) (net.Listener, error)

type Server struct {
	ServerOptions
	*routeGroup

	upgrader            *websocket.Upgrader
	currentConnectionID uint64
	router              *router
	workerPool          *workerPool
	decoder             DecoderInterface
	encoder             EncoderInterface
	connectionManager   *connectionManager
	metrics             serverMetrics

	connectionOpen  func(Connection)
	connectionClose func(Connection)
	connectionError ConnectionErrorHandler
	runtimeRoutes   map[uint32][]Handler
	runtimeOpen     func(Connection)
	runtimeClose    func(Connection)
	runtimeError    ConnectionErrorHandler

	stateMu       sync.Mutex
	state         serverState
	startupDone   chan struct{}
	shutdownDone  chan struct{}
	serveGate     chan struct{}
	stopRequested bool
	shutdownErr   error
	runtimeErr    chan error

	startupCloseOnce  sync.Once
	shutdownCloseOnce sync.Once
	serveGateOnce     sync.Once
	shutdownStartOnce sync.Once

	tcpListen       listenFunc
	webSocketListen listenFunc
	listeners       map[Transport]net.Listener
	addresses       map[Transport]net.Addr
	webSocketServer *http.Server
	serviceWG       sync.WaitGroup
	setupMu         sync.Mutex
	acceptingSetups bool
	setupWG         sync.WaitGroup
}

func NewServer(serverOptions ...ServerOption) (*Server, error) {
	opts := defaultServerOptions()
	for _, option := range serverOptions {
		option(&opts)
	}
	if err := validateServerOptions(opts); err != nil {
		return nil, err
	}
	if _, err := NewFrameDecoder(
		WithLengthFieldOffset(4),
		WithLengthFieldLength(4),
		WithMaxFrameLength(opts.MaxFrameLength),
	); err != nil {
		return nil, err
	}

	server := &Server{
		ServerOptions:   opts,
		decoder:         &Decoder{},
		encoder:         &Encoder{},
		state:           stateNew,
		tcpListen:       net.Listen,
		webSocketListen: net.Listen,
	}
	server.upgrader = &websocket.Upgrader{
		ReadBufferSize: int(server.ConnectionReadBufferSize),
		CheckOrigin:    func(*http.Request) bool { return true },
	}
	server.router = newRouter()
	routeGroup := newGroup(server.router)
	routeGroup.server = server
	server.routeGroup = routeGroup
	server.connectionManager = newConnectionManager(server.ConnectionGroupsCount)
	server.workerPool = newWorkerPool(server.WorkerCount, server.WorkerQueueCapacity)
	return server, nil
}

func (s *Server) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.claimRun(); err != nil {
		return err
	}
	if err := validateServerOptions(s.ServerOptions); err != nil {
		s.rollbackStartup()
		return err
	}
	if _, err := NewFrameDecoder(
		WithLengthFieldOffset(4),
		WithLengthFieldLength(4),
		WithMaxFrameLength(s.MaxFrameLength),
	); err != nil {
		s.rollbackStartup()
		return err
	}

	s.runtimeRoutes = s.router.freeze()
	s.runtimeOpen = s.connectionOpen
	s.runtimeClose = s.connectionClose
	s.runtimeError = s.connectionError
	s.connectionManager = newConnectionManager(s.ConnectionGroupsCount)
	s.workerPool = newWorkerPool(s.WorkerCount, s.WorkerQueueCapacity)
	if err := s.prepareWebSocketServer(); err != nil {
		s.rollbackStartup()
		return err
	}

	if canceled := s.startupCanceled(ctx); canceled {
		s.rollbackStartup()
		return nil
	}
	if err := s.bindTransports(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			s.rollbackStartup()
			return nil
		}
		s.rollbackStartup()
		return err
	}
	if err := s.applyTLS(); err != nil {
		s.rollbackStartup()
		return err
	}
	if s.startupCanceled(ctx) {
		s.rollbackStartup()
		return nil
	}

	s.stateMu.Lock()
	if s.stopRequested || ctx.Err() != nil {
		s.stateMu.Unlock()
		s.rollbackStartup()
		return nil
	}
	s.workerPool.start()
	s.setupMu.Lock()
	s.acceptingSetups = true
	s.setupMu.Unlock()
	s.launchServices()
	s.state = stateRunning
	closeOnce(&s.serveGateOnce, s.serveGate)
	closeOnce(&s.startupCloseOnce, s.startupDone)
	s.stateMu.Unlock()

	var runtimeCause error
	select {
	case <-ctx.Done():
		s.startShutdown()
	case runtimeCause = <-s.runtimeErr:
		s.startShutdown()
	case <-s.shutdownDone:
	}
	<-s.shutdownDone
	if runtimeCause == nil {
		select {
		case runtimeCause = <-s.runtimeErr:
		default:
		}
	}
	if runtimeCause != nil {
		wrappedRuntime := fmt.Errorf("server runtime failure: %w", runtimeCause)
		if shutdownErr := s.terminalShutdownError(); shutdownErr != nil {
			return errors.Join(wrappedRuntime, shutdownErr)
		}
		return wrappedRuntime
	}
	return s.terminalShutdownError()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	s.stateMu.Lock()
	switch s.state {
	case stateNew:
		s.stateMu.Unlock()
		return nil
	case stateStarting:
		s.stopRequested = true
		startupDone := s.startupDone
		s.stateMu.Unlock()
		select {
		case <-startupDone:
			return s.terminalShutdownError()
		case <-ctx.Done():
			return ctx.Err()
		}
	case stateRunning:
		s.state = stateStopping
		shutdownDone := s.shutdownDone
		s.stateMu.Unlock()
		s.shutdownStartOnce.Do(func() { go s.shutdownOwner() })
		select {
		case <-shutdownDone:
			return s.terminalShutdownError()
		case <-ctx.Done():
			return ctx.Err()
		}
	case stateStopping:
		shutdownDone := s.shutdownDone
		s.stateMu.Unlock()
		select {
		case <-shutdownDone:
			return s.terminalShutdownError()
		case <-ctx.Done():
			return ctx.Err()
		}
	case stateStopped:
		err := s.shutdownErr
		s.stateMu.Unlock()
		return err
	default:
		s.stateMu.Unlock()
		return nil
	}
}

func (s *Server) claimRun() error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	switch s.state {
	case stateNew:
		s.state = stateStarting
		s.startupDone = make(chan struct{})
		s.shutdownDone = make(chan struct{})
		s.serveGate = make(chan struct{})
		s.runtimeErr = make(chan error, len(s.Transports))
		s.listeners = make(map[Transport]net.Listener)
		s.addresses = make(map[Transport]net.Addr)
		return nil
	case stateStopped:
		return ErrServerStopped
	default:
		return ErrServerRunning
	}
}

func (s *Server) startupCanceled(ctx context.Context) bool {
	s.stateMu.Lock()
	stopRequested := s.stopRequested
	s.stateMu.Unlock()
	return stopRequested || ctx.Err() != nil
}

func (s *Server) bindTransports(ctx context.Context) error {
	for _, transport := range s.Transports {
		if s.startupCanceled(ctx) {
			return context.Canceled
		}
		var (
			listener net.Listener
			err      error
		)
		switch transport {
		case TransportTCP:
			listener, err = s.tcpListen(s.IPVersion, fmt.Sprintf("%s:%d", s.IP, s.Port))
		case TransportWebSocket:
			listener, err = s.webSocketListen("tcp", fmt.Sprintf("%s:%d", s.IP, s.WebSocketPort))
		}
		if err != nil {
			return err
		}
		s.listeners[transport] = listener
		s.stateMu.Lock()
		s.addresses[transport] = listener.Addr()
		s.stateMu.Unlock()
	}
	return nil
}

func (s *Server) applyTLS() error {
	if s.CertFile == "" {
		return nil
	}
	certificate, err := tls.LoadX509KeyPair(s.CertFile, s.PrivateKeyFile)
	if err != nil {
		return err
	}
	config := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	for transport, listener := range s.listeners {
		s.listeners[transport] = tls.NewListener(listener, config)
	}
	return nil
}

func (s *Server) prepareWebSocketServer() (err error) {
	if !s.HasTransport(TransportWebSocket) {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: invalid websocket path %q: %v", ErrInvalidConfiguration, s.WebSocketPath, recovered)
		}
	}()
	mux := http.NewServeMux()
	mux.HandleFunc(s.WebSocketPath, s.handleWebSocketUpgrade)
	s.webSocketServer = &http.Server{Handler: mux}
	return nil
}

func (s *Server) launchServices() {
	for transport, listener := range s.listeners {
		s.serviceWG.Add(1)
		go func(transport Transport, listener net.Listener) {
			defer s.serviceWG.Done()
			<-s.serveGate
			var err error
			switch transport {
			case TransportTCP:
				err = s.serveTCP(listener)
			case TransportWebSocket:
				err = s.serveWebSocket(listener)
			}
			if err != nil {
				select {
				case s.runtimeErr <- err:
				default:
				}
			}
		}(transport, listener)
	}
}

func (s *Server) rollbackStartup() {
	closeOnce(&s.serveGateOnce, s.serveGate)
	s.closeServingResources()
	s.serviceWG.Wait()
	s.stateMu.Lock()
	s.state = stateStopped
	s.shutdownErr = nil
	closeOnce(&s.startupCloseOnce, s.startupDone)
	closeOnce(&s.shutdownCloseOnce, s.shutdownDone)
	s.stateMu.Unlock()
}

func (s *Server) startShutdown() {
	s.stateMu.Lock()
	if s.state == stateRunning {
		s.state = stateStopping
	}
	s.stateMu.Unlock()
	s.shutdownStartOnce.Do(func() { go s.shutdownOwner() })
}

func (s *Server) shutdownOwner() {
	ctx, cancel := context.WithTimeout(context.Background(), s.ShutdownTimeout)
	defer cancel()
	timedOut := false

	s.workerPool.stopAccepting()
	s.stopConnectionSetups()
	s.closeServingResources()
	if !s.waitForConnectionSetups(ctx) {
		timedOut = true
	}
	_ = s.connectionManager.quiesceAll()

	if err := s.workerPool.stopAcceptingAndDrain(ctx); err != nil {
		timedOut = true
	}
	for _, connection := range s.connectionManager.snapshot() {
		if err := connection.stopSendsAndDrain(ctx); err != nil {
			timedOut = true
			break
		}
	}
	s.connectionManager.forceCloseAll()
	if err := s.connectionManager.waitAll(ctx); err != nil {
		timedOut = true
	}

	servicesDone := make(chan struct{})
	go func() {
		s.serviceWG.Wait()
		close(servicesDone)
	}()
	select {
	case <-servicesDone:
	case <-ctx.Done():
		timedOut = true
	}

	if timedOut {
		s.workerPool.forceCancel()
		s.connectionManager.forceCloseAll()
	}

	s.stateMu.Lock()
	if timedOut {
		s.shutdownErr = fmt.Errorf("%w: %v", ErrShutdownTimeout, ctx.Err())
	}
	s.state = stateStopped
	closeOnce(&s.shutdownCloseOnce, s.shutdownDone)
	s.stateMu.Unlock()
}

func (s *Server) closeServingResources() {
	if s.webSocketServer != nil {
		_ = s.webSocketServer.Close()
	}
	for _, listener := range s.listeners {
		_ = listener.Close()
	}
}

func (s *Server) beginConnectionSetup() bool {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if !s.acceptingSetups {
		return false
	}
	s.setupWG.Add(1)
	return true
}

func (s *Server) finishConnectionSetup() {
	s.setupWG.Done()
}

func (s *Server) stopConnectionSetups() {
	s.setupMu.Lock()
	s.acceptingSetups = false
	s.setupMu.Unlock()
}

func (s *Server) waitForConnectionSetups(ctx context.Context) bool {
	done := make(chan struct{})
	go func() {
		s.setupWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Server) terminalShutdownError() error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.shutdownErr
}

func (s *Server) handleRequest(connection Connection, request *Request) error {
	parent := context.Background()
	if provider, ok := connection.(interface{ taskContext() context.Context }); ok && provider.taskContext() != nil {
		parent = provider.taskContext()
	}
	ctx := newContext(parent, connection, request)
	routes := s.runtimeRoutes
	if routes == nil {
		routes = s.router.freeze()
	}
	if handlers, ok := routes[ctx.Request.Message.Event]; ok {
		ctx.handlers = append(ctx.handlers, handlers...)
	} else {
		ctx.handlers = append(ctx.handlers, func(ctx *Context) {
			_ = ctx.Connection.Send(ctx, 404, []byte("Event Not Found"))
		})
	}
	if err := s.workerPool.submit(ctx); err != nil {
		ctx.finish()
		return err
	}
	return nil
}

func (s *Server) OnConnectionOpen(callback func(Connection)) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if err := s.mutationErrorLocked(); err != nil {
		return err
	}
	s.connectionOpen = callback
	return nil
}

func (s *Server) OnConnectionClose(callback func(Connection)) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if err := s.mutationErrorLocked(); err != nil {
		return err
	}
	s.connectionClose = callback
	return nil
}

func (s *Server) OnConnectionError(callback ConnectionErrorHandler) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if err := s.mutationErrorLocked(); err != nil {
		return err
	}
	s.connectionError = callback
	return nil
}

func (s *Server) invokeOpenHook(connection managedConnection) {
	callback := s.runtimeOpen
	if callback == nil {
		callback = s.connectionOpen
	}
	if callback == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err := fmt.Errorf("open hook panic: %v", recovered)
			s.reportConnectionError(connection, OperationOpenHook, err)
			connection.requestClose(OperationOpenHook, err)
		}
	}()
	callback(connection)
}

func (s *Server) invokeCloseHook(connection Connection) {
	callback := s.runtimeClose
	if callback == nil {
		callback = s.connectionClose
	}
	if callback == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			s.reportConnectionError(connection, OperationCloseHook, fmt.Errorf("close hook panic: %v", recovered))
		}
	}()
	callback(connection)
}

func (s *Server) reportConnectionError(connection Connection, operation ConnectionOperation, err error) {
	if err == nil {
		return
	}
	callback := s.runtimeError
	if callback == nil {
		callback = s.connectionError
	}
	if callback == nil {
		debug("Connection %d %s error: %v", connection.ID(), operation, err)
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			debug("Connection error handler panic: %v", recovered)
		}
	}()
	callback(connection, operation, err)
}

func (s *Server) openWebSocketConnection(socket *websocket.Conn, connectionID uint64) {
	base, err := newNetConnection(connectionID, s, socket, func(data []byte) error {
		return socket.WriteMessage(websocket.BinaryMessage, data)
	})
	if err != nil {
		_ = socket.Close()
		return
	}
	connection := &WebSocketConnection{socket: socket, netConnection: base}
	s.connectionManager.addConnection(connection)
	connection.open()
}

func (s *Server) openTCPConnection(socket net.Conn, connectionID uint64) {
	base, err := newNetConnection(connectionID, s, socket, func(data []byte) error {
		return writeFull(socket, data)
	})
	if err != nil {
		_ = socket.Close()
		return
	}
	connection := &TCPConnection{socket: socket, netConnection: base}
	s.connectionManager.addConnection(connection)
	connection.open()
}

func (s *Server) nextConnectionID() uint64 {
	return atomic.AddUint64(&s.currentConnectionID, 1)
}

func (s *Server) expectedServingError(err error) bool {
	return err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, http.ErrServerClosed)
}

func (s *Server) Address(transport Transport) net.Addr {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.addresses[transport]
}
