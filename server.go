package ramix

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type Server struct {
	ServerOptions
	routeGroup

	ctx    context.Context
	cancel context.CancelFunc

	router *router
	queue  *queue

	decoder DecoderInterface
	encoder EncoderInterface

	heartbeatChecker *heartbeatChecker

	connectionManager *connectionManager
	connectionOpen    func(connection *Connection)
	connectionClose   func(connection *Connection)
}

func (s *Server) Serve() {
	go s.listen()
	go s.monitor()

	<-s.stop()
}

func (s *Server) listen() {
	addr, err := net.ResolveTCPAddr(s.IPVersion, fmt.Sprintf("%s:%d", s.IP, s.Port))

	if err != nil {
		debug("Resolve TCP Address error: %v", err)
		return
	}

	s.queue.start()

	listener, err := net.ListenTCP(s.IPVersion, addr)

	if err != nil {
		debug("Listen TCP error: %v", err)
		return
	}

	debug("Server started, Listening on: %s:%d", s.IP, s.Port)

	var connectionID uint64

	for {
		select {
		case <-s.ctx.Done():
			debug("Server listener stopped")
			return
		default:
			socket, err := listener.AcceptTCP()

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

			go s.OpenConnection(socket, connectionID)

			connectionID++
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

			s.queue.close()
			s.connectionManager.clearConnections()

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

func (s *Server) OpenConnection(socket *net.TCPConn, connectionID uint64) {
	connection := &Connection{
		ID:             connectionID,
		socket:         socket,
		isClosed:       false,
		messageChannel: make(chan []byte),
		server:         s,
		frameDecoder: NewFrameDecoder(
			WithLengthFieldOffset(4),
			WithLengthFieldLength(4),
		),
	}

	connection.ctx, connection.cancel = context.WithCancel(context.Background())

	if s.heartbeatChecker != nil {
		connection.heartbeatChecker = s.heartbeatChecker.clone(connection)
	}

	s.connectionManager.addConnection(connection)

	connection.open()
}

func (s *Server) handleRequest(connection *Connection, request *Request) {
	ctx := &Context{
		Connection: connection,
		Request:    request,
		step:       -1,
	}

	if handlers, ok := s.router.routes[ctx.Request.Message.Event]; ok {
		ctx.handlers = append(ctx.handlers, handlers...)
	} else {
		ctx.handlers = append(ctx.handlers, func(context *Context) {
			_ = context.Connection.SendMessage(404, []byte("Event Not Found"))
		})
	}

	if s.queue.workersCount > 0 {
		s.queue.contextChannel <- ctx
	} else {
		go func(context *Context) {
			context.Next()
		}(ctx)
	}
}

func (s *Server) OnConnectionOpen(callback func(connection *Connection)) {
	s.connectionOpen = callback
}

func (s *Server) OnConnectionClose(callback func(connection *Connection)) {
	s.connectionClose = callback
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

	server.ctx, server.cancel = context.WithCancel(context.Background())

	server.router = &router{
		routes: make(map[uint32][]HandlerInterface),
	}

	server.routeGroup = routeGroup{
		router: server.router,
	}

	server.queue = &queue{
		contextChannel: make(chan *Context, server.MaxTasksCount),
		workersCount:   server.WorkersCount,
		ctx:            server.ctx,
	}

	server.connectionManager = &connectionManager{
		connections: make(map[uint64]*Connection),
	}

	server.heartbeatChecker = &heartbeatChecker{
		interval: server.HeartbeatInterval,
		handler: func(connection *Connection) {
			if connection.isAlive() {
				return
			}

			connection.close(true)
		},
	}

	return server
}
