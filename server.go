package ramix

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/gorilla/websocket"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

type Server struct {
	ServerOptions
	*routeGroup
	upgrader            *websocket.Upgrader
	currentConnectionID uint64
	ctx                 context.Context
	cancel              context.CancelFunc
	router              *router
	workerPool          WorkerPool
	decoder             DecoderInterface
	encoder             EncoderInterface
	heartbeatChecker    *heartbeatChecker
	connectionManager   *connectionManager
	connectionOpen      func(connection Connection)
	connectionClose     func(connection Connection)
}

func (s *Server) Serve() {
	if s.UsingWorkerPool() {
		s.startWorkerPool()
	}

	switch {
	case s.OnlyTCP:
		go s.listenTCP()
	case s.OnlyWebSocket:
		go s.listenWebSocket()
	default:
		go s.listenTCP()
		go s.listenWebSocket()
	}

	go s.monitor()

	<-s.stop()
}

func (s *Server) listenWebSocket() {
	if s.WebSocketPath == "" {
		panic("WebSocket path is empty")
	}

	http.HandleFunc(s.WebSocketPath, func(writer http.ResponseWriter, request *http.Request) {
		socket, err := s.upgrader.Upgrade(writer, request, nil)

		if err != nil {
			debug("WebSocket upgrade error: %v", err)
			return
		}

		atomic.AddUint64(&s.currentConnectionID, 1)

		go s.openWebSocketConnection(socket, s.currentConnectionID)
	})

	debug("WebSocket server is starting on %s:%d", s.IP, s.WebSocketPort)

	if s.CertFile != "" && s.PrivateKeyFile != "" {
		if err := http.ListenAndServeTLS(fmt.Sprintf("%s:%d", s.IP, s.WebSocketPort), s.CertFile, s.PrivateKeyFile, nil); err != nil {
			panic(fmt.Sprintf("Listen and serve TLS error: %v", err))
		}
	} else {
		if err := http.ListenAndServe(fmt.Sprintf("%s:%d", s.IP, s.WebSocketPort), nil); err != nil {
			panic(fmt.Sprintf("Listen and serve error: %v", err))
		}
	}
}

func (s *Server) listenTCP() {
	var listener net.Listener

	debug("TCP server is starting on %s:%d", s.IP, s.Port)

	if s.CertFile != "" && s.PrivateKeyFile != "" {
		certificate, err := tls.LoadX509KeyPair(s.CertFile, s.PrivateKeyFile)

		if err != nil {
			panic(fmt.Sprintf("Load X509 key pair error: %v", err))
		}

		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{certificate},
		}

		listener, err = tls.Listen(s.IPVersion, fmt.Sprintf("%s:%d", s.IP, s.Port), tlsConfig)

		if err != nil {
			panic(fmt.Sprintf("Listen TLS error: %v", err))
		}
	} else {
		var err error

		listener, err = net.Listen(s.IPVersion, fmt.Sprintf("%s:%d", s.IP, s.Port))

		if err != nil {
			panic(fmt.Sprintf("Listen TCP error: %v", err))
		}
	}

	for {
		select {
		case <-s.ctx.Done():
			debug("Server listener stopped")
			return
		default:
			socket, err := listener.Accept()

			if err != nil {
				debug("Accept error: %v", err)
				continue
			}

			debug("Accept a connection: %v", socket.RemoteAddr())

			if s.connectionManager.connectionsCount() >= s.MaxConnectionsCount {
				_ = socket.Close()
				debug("Connections count exceeds the limit: %d", s.MaxConnectionsCount)
				continue
			}

			atomic.AddUint64(&s.currentConnectionID, 1)

			go s.openTCPConnection(socket, s.currentConnectionID)
		}
	}
}

func (s *Server) stop() <-chan struct{} {
	done := make(chan struct{})
	signals := make(chan os.Signal, 2)

	signal.Notify(signals, syscall.SIGQUIT, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-signals

		go func() {
			debug("Server stopping...")

			s.cancel()

			s.stopWorkerPool()
			s.clearConnections()

			debug("Server stopped")

			done <- struct{}{}
		}()

		<-signals
		debug("Server force stopping...")
		os.Exit(128 + int(sig.(syscall.Signal)))
	}()

	return done
}

func (s *Server) monitor() {
	for {
		select {
		case <-s.ctx.Done():
			debug("Server monitor stopped")
			return
		case <-time.After(time.Second * 3):
			debug("Connections count: %d\n", s.connectionManager.connectionsCount())
		}
	}
}

func (s *Server) startWorkerPool() {
	s.workerPool.init()
	s.workerPool.start()
}

func (s *Server) stopWorkerPool() {
	s.workerPool.stop()
}

func (s *Server) clearConnections() {
	s.connectionManager.clearConnections()
}

func (s *Server) openWebSocketConnection(socket *websocket.Conn, connectionID uint64) {
	c := newNetConnection(connectionID, s)

	connection := &WebSocketConnection{
		socket:        socket,
		netConnection: c,
	}

	connection.ctx, connection.cancel = context.WithCancel(context.Background())

	if !s.UsingWorkerPool() {
		w := newWorker(int(connectionID), s.MaxWorkerTasksCount)
		w.start()
		c.worker = w
	}

	if s.heartbeatChecker != nil {
		connection.heartbeatChecker = s.heartbeatChecker.clone(connection)
	}

	s.connectionManager.addConnection(connection)

	connection.open()

	debug("WebSocketConnection %d opened, worker %d assigned", connection.ID(), connection.worker.id)
}

func (s *Server) openTCPConnection(socket net.Conn, connectionID uint64) {
	c := newNetConnection(connectionID, s)

	connection := &TCPConnection{
		socket:        socket,
		netConnection: c,
	}

	connection.ctx, connection.cancel = context.WithCancel(context.Background())

	if !s.UsingWorkerPool() {
		w := newWorker(int(connectionID), s.MaxWorkerTasksCount)
		w.start()
		c.worker = w
	}

	if s.heartbeatChecker != nil {
		connection.heartbeatChecker = s.heartbeatChecker.clone(connection)
	}

	s.connectionManager.addConnection(connection)

	connection.open()

	debug("TCPConnection %d opened", connection.ID())
}

func (s *Server) handleRequest(connection Connection, request *Request) {
	ctx := newContext(connection, request)

	if handlers, ok := s.router.routes[ctx.Request.Message.Event]; ok {
		ctx.handlers = append(ctx.handlers, handlers...)
	} else {
		ctx.handlers = append(ctx.handlers, func(context *Context) {
			_ = context.Connection.SendMessage(404, []byte("Event Not Found"))
		})
	}

	if s.UsingWorkerPool() {
		s.workerPool.submitTask(ctx)
	} else {
		connection.submitTask(ctx)
	}
}

func (s *Server) OnConnectionOpen(callback func(connection Connection)) {
	s.connectionOpen = callback
}

func (s *Server) OnConnectionClose(callback func(connection Connection)) {
	s.connectionClose = callback
}

func (s *Server) UseWorkerPool(workerPool WorkerPool) {
	s.workerPool = workerPool
}

func (s *Server) UsingWorkerPool() bool {
	return s.workerPool != nil
}

func NewServer(serverOptions ...ServerOption) *Server {
	server := &Server{
		ServerOptions: defaultServerOptions,
		decoder:       &Decoder{},
		encoder:       &Encoder{},
	}

	for _, option := range serverOptions {
		option(&server.ServerOptions)
	}

	server.upgrader = &websocket.Upgrader{
		ReadBufferSize: int(server.ConnectionReadBufferSize),
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	server.ctx, server.cancel = context.WithCancel(context.Background())

	server.router = newRouter()
	server.routeGroup = newGroup(server.router)
	server.connectionManager = newConnectionManager(server.ConnectionGroupsCount)
	server.heartbeatChecker = newHeartbeatChecker(server.HeartbeatInterval, nil)

	return server
}
