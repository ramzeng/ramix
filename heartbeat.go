package ramix

import (
	"context"
	"time"
)

func newHeartbeatChecker(interval time.Duration, handler func(connection *Connection)) *heartbeatChecker {
	if handler == nil {
		handler = func(connection *Connection) {
			if connection.isAlive() {
				return
			}

			connection.close(true)
		}
	}

	return &heartbeatChecker{
		interval: interval,
		handler:  handler,
	}
}

type heartbeatChecker struct {
	connection *Connection
	interval   time.Duration
	handler    func(connection *Connection)
	ctx        context.Context
	cancel     context.CancelFunc
}

func (h *heartbeatChecker) start() {
	h.connection.RefreshLastActiveTime()

	ticker := time.NewTicker(h.interval)

	for {
		select {
		case <-h.ctx.Done():
			ticker.Stop()
			debug("Connection %d heartbeat checker stopped", h.connection.ID)
			return
		case <-ticker.C:
			h.handler(h.connection)
		}
	}
}

func (h *heartbeatChecker) stop() {
	h.cancel()
}

func (h *heartbeatChecker) clone(connection *Connection) *heartbeatChecker {
	checker := &heartbeatChecker{
		connection: connection,
		interval:   h.interval,
		handler:    h.handler,
	}

	checker.ctx, checker.cancel = context.WithCancel(context.Background())

	return checker
}
