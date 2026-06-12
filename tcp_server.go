package ramix

import (
	"net"
)

func (s *Server) serveTCP(listener net.Listener) error {
	for {
		socket, err := listener.Accept()
		if err != nil {
			if s.expectedServingError(err) {
				return nil
			}
			return err
		}
		if s.connectionManager.connectionsCount() >= s.MaxConnectionsCount {
			_ = socket.Close()
			continue
		}
		if !s.beginConnectionSetup() {
			_ = socket.Close()
			continue
		}
		s.openTCPConnection(socket, s.nextConnectionID())
		s.finishConnectionSetup()
	}
}
