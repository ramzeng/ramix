package ramix

import (
	"context"
	"fmt"
	"net"
	"time"
)

type Server struct {
	ServerOptions
	routeGroup

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
	select {}
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

func (s *Server) Stop() {

}

func (s *Server) monitor() {
	for {
		select {
		case <-time.After(time.Second * 3):
			debug("Connections count: %d\n", s.connectionManager.connectionsCount())
		}
	}
}

func (s *Server) OpenConnection(socket *net.TCPConn, connectionID uint64) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)

	connection := &Connection{
		ID:             connectionID,
		socket:         socket,
		isClosed:       false,
		ctx:            ctx,
		cancel:         cancel,
		messageChannel: make(chan []byte),
		server:         s,
		frameDecoder: NewFrameDecoder(
			WithLengthFieldOffset(4),
			WithLengthFieldLength(4),
		),
	}

	if s.heartbeatChecker != nil {
		connection.heartbeatChecker = s.heartbeatChecker.clone(connection)
	}

	s.connectionManager.addConnection(connection)

	connection.open()
}

func (s *Server) handleRequest(connection *Connection, request *Request) {
	context := &Context{
		Connection: connection,
		Request:    request,
		step:       -1,
	}

	if handlers, ok := s.router.routes[context.Request.Message.Event]; ok {
		context.handlers = append(context.handlers, handlers...)
	} else {
		context.handlers = append(context.handlers, func(context *Context) {
			_ = context.Connection.SendMessage(404, []byte("Event Not Found"))
		})
	}

	if s.queue.workersCount > 0 {
		s.queue.contextChannel <- context
	} else {
		go func(context *Context) {
			context.Next()
		}(context)
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

	server.router = &router{
		routes: make(map[uint32][]HandlerInterface),
	}

	server.routeGroup = routeGroup{
		router: server.router,
	}

	server.queue = &queue{
		contextChannel: make(chan *Context, server.MaxTasksCount),
		workersCount:   server.WorkersCount,
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

			connection.close()
		},
	}

	return server
}
