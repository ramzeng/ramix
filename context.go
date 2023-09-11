package ramix

import "sync"

type Context struct {
	Connection *Connection
	Request    *Request

	handlers []Handler
	step     int
	keys     map[string]any
	lock     sync.RWMutex
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
