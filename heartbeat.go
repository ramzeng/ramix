package ramix

import (
	"context"
	"sync/atomic"
	"time"
)

type activityClock struct {
	now        func() time.Time
	lastActive atomic.Int64
}

func newActivityClock(now func() time.Time) *activityClock {
	if now == nil {
		now = time.Now
	}
	return &activityClock{now: now}
}

func (c *activityClock) refresh() {
	c.lastActive.Store(c.now().UnixNano())
}

func (c *activityClock) alive(timeout time.Duration) bool {
	lastActive := time.Unix(0, c.lastActive.Load())
	return lastActive.Add(timeout).After(c.now())
}

func (c *netConnection) refreshActivity() {
	c.activity.refresh()
}

func (c *netConnection) checkHeartbeat() {
	if c.activity.alive(c.server.HeartbeatTimeout) {
		return
	}
	c.requestCloseIfOpen(OperationHeartbeat, context.DeadlineExceeded)
}

func (c *netConnection) runHeartbeat() {
	ticker := time.NewTicker(c.server.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.readCtx.Done():
			return
		case <-c.forceCtx.Done():
			return
		case <-ticker.C:
			c.checkHeartbeat()
		}
	}
}
