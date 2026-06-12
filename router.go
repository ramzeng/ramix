package ramix

import "sync"

type Handler func(context *Context)

type router struct {
	mu     sync.RWMutex
	routes map[uint32][]Handler
}

type routeGroup struct {
	server   *Server
	router   *router
	parent   *routeGroup
	handlers []Handler
}

func newRouter() *router {
	return &router{routes: make(map[uint32][]Handler)}
}

func newGroup(router *router) *routeGroup {
	return &routeGroup{router: router}
}

func (g *routeGroup) Group() *routeGroup {
	g.router.mu.RLock()
	handlers := append([]Handler(nil), g.handlers...)
	g.router.mu.RUnlock()
	return &routeGroup{
		server:   g.server,
		router:   g.router,
		parent:   g,
		handlers: handlers,
	}
}

func (g *routeGroup) Use(handlers ...Handler) error {
	if g.server != nil {
		g.server.stateMu.Lock()
		defer g.server.stateMu.Unlock()
		if err := g.server.mutationErrorLocked(); err != nil {
			return err
		}
	}
	g.router.mu.Lock()
	g.handlers = append(g.handlers, handlers...)
	g.router.mu.Unlock()
	return nil
}

func (g *routeGroup) RegisterRoute(event uint32, handler Handler) error {
	if g.server != nil {
		g.server.stateMu.Lock()
		defer g.server.stateMu.Unlock()
		if err := g.server.mutationErrorLocked(); err != nil {
			return err
		}
	}
	g.router.mu.Lock()
	handlers := append([]Handler(nil), g.handlers...)
	g.router.routes[event] = append(handlers, handler)
	g.router.mu.Unlock()
	return nil
}

func (r *router) freeze() map[uint32][]Handler {
	r.mu.RLock()
	defer r.mu.RUnlock()

	frozen := make(map[uint32][]Handler, len(r.routes))
	for event, handlers := range r.routes {
		frozen[event] = append([]Handler(nil), handlers...)
	}
	return frozen
}
