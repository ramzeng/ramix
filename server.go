package ramix

import (
	"fmt"
	"net"
	"time"
)

type ServerConfig struct {
	Name                string
	IPVersion           string
	IP                  string
	Port                int
	MaxConnectionsCount int
	MaxReadBufferSize   uint32
	MaxMessageSize      uint32
	MaxTasksCount       uint32
	WorkersCount        uint32
	HeartbeatInterval   time.Duration
	HeartbeatTimeout    time.Duration
}

type Server struct {
	ServerConfig
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

		if s.connectionManager.ConnectionsCount() >= s.MaxConnectionsCount {
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
			debug("Connections count: %d\n", s.connectionManager.ConnectionsCount())
		}
	}
}

func (s *Server) OpenConnection(socket *net.TCPConn, connectionID uint64) {
	connection := &Connection{
		ID:             connectionID,
		socket:         socket,
		isClosed:       false,
		messageChannel: make(chan []byte),
		quitSignal:     make(chan struct{}),
		server:         s,
	}

	if s.heartbeatChecker != nil {
		connection.heartbeatChecker = s.heartbeatChecker.clone(connection)
	}

	s.connectionManager.AddConnection(connection)

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

func NewServer(serverConfig ServerConfig) *Server {
	server := &Server{
		ServerConfig: serverConfig,
		decoder:      &Decoder{},
		encoder:      &Encoder{},
	}

	server.router = &router{
		routes: make(map[uint32][]HandlerInterface),
	}

	server.routeGroup = routeGroup{
		router: server.router,
	}

	server.queue = &queue{
		contextChannel: make(chan *Context, serverConfig.MaxTasksCount),
		workersCount:   serverConfig.WorkersCount,
	}

	server.connectionManager = &connectionManager{
		connections: make(map[uint64]*Connection),
	}

	server.heartbeatChecker = &heartbeatChecker{
		interval: serverConfig.HeartbeatInterval,
		handler: func(connection *Connection) {
			if connection.isAlive() {
				return
			}

			connection.close()
		},
		quitSignal: make(chan struct{}),
	}

	return server
}
