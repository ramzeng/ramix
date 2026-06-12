package ramix

import (
	"net"
	"net/http"
)

func (s *Server) serveWebSocket(listener net.Listener) error {
	err := s.webSocketServer.Serve(listener)
	if s.expectedServingError(err) {
		return nil
	}
	return err
}

func (s *Server) handleWebSocketUpgrade(writer http.ResponseWriter, request *http.Request) {
	if !s.beginConnectionSetup() {
		http.Error(writer, "server stopping", http.StatusServiceUnavailable)
		return
	}
	defer s.finishConnectionSetup()
	if s.connectionManager.connectionsCount() >= s.MaxConnectionsCount {
		http.Error(writer, "connection limit reached", http.StatusServiceUnavailable)
		return
	}
	socket, err := s.upgrader.Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	s.openWebSocketConnection(socket, s.nextConnectionID())
}
