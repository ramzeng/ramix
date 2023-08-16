package ramix

import (
	"context"
	"time"
)

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
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)

	return &heartbeatChecker{
		connection: connection,
		interval:   h.interval,
		handler:    h.handler,
		ctx:        ctx,
		cancel:     cancel,
	}
}
