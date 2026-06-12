package ramix

import (
	"context"
	"sync"
)

type Context struct {
	context.Context
	Connection Connection
	Request    *Request

	handlers []Handler
	step     int
	keys     map[string]any
	lock     sync.RWMutex
	cancel   context.CancelFunc
	finishMu sync.Once
}

func (c *Context) Next() {
	c.step++

	for ; c.step < len(c.handlers); c.step++ {
		c.handlers[c.step](c)
	}
}

func (c *Context) Set(key string, value any) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if c.keys == nil {
		c.keys = make(map[string]any)
	}

	c.keys[key] = value
}

func (c *Context) Get(key string) any {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return c.keys[key]
}

func (c *Context) cancelTask() {
	if c.cancel != nil {
		c.cancel()
	}
}

func (c *Context) finish() {
	c.finishMu.Do(func() {
		c.cancelTask()
	})
}

func newContext(parent context.Context, connection Connection, request *Request) *Context {
	if parent == nil {
		parent = context.Background()
	}

	ctx, cancel := context.WithCancel(parent)

	return &Context{
		Context:    ctx,
		Connection: connection,
		Request:    request,
		step:       -1,
		cancel:     cancel,
	}
}
