package ramix

import "time"

type heartbeatChecker struct {
	connection *Connection
	interval   time.Duration
	handler    func(connection *Connection)
	quitSignal chan struct{}
}

func (h *heartbeatChecker) start() {
	h.connection.RefreshLastActiveTime()

	ticker := time.NewTicker(h.interval)

	for {
		select {
		case <-h.quitSignal:
			ticker.Stop()
			return
		case <-ticker.C:
			h.handler(h.connection)
		}
	}
}

func (h *heartbeatChecker) stop() {
	h.quitSignal <- struct{}{}
	close(h.quitSignal)
}

func (h *heartbeatChecker) clone(connection *Connection) *heartbeatChecker {
	return &heartbeatChecker{
		connection: connection,
		interval:   h.interval,
		handler:    h.handler,
		quitSignal: make(chan struct{}),
	}
}
