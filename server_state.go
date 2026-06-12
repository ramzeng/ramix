package ramix

import "sync"

type serverState uint8

const (
	stateNew serverState = iota
	stateStarting
	stateRunning
	stateStopping
	stateStopped
)

func (s *Server) currentState() serverState {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.state
}

func (s *Server) mutationErrorLocked() error {
	switch s.state {
	case stateNew:
		return nil
	case stateStopped:
		return ErrServerStopped
	default:
		return ErrServerRunning
	}
}

func closeOnce(once *sync.Once, channel chan struct{}) {
	once.Do(func() { close(channel) })
}
