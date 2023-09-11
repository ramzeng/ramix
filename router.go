package ramix

type Handler func(context *Context)

func newRouter() *router {
	return &router{
		routes: make(map[uint32][]Handler),
	}
}

func newGroup(router *router) *routeGroup {
	return &routeGroup{
		router: router,
	}
}

type router struct {
	routes map[uint32][]Handler
}

type routeGroup struct {
	router   *router
	parent   *routeGroup
	handlers []Handler
}

func (rg *routeGroup) Group() *routeGroup {
	group := &routeGroup{
		router:   rg.router,
		parent:   rg,
		handlers: rg.handlers,
	}

	return group
}

func (rg *routeGroup) Use(handlers ...Handler) *routeGroup {
	rg.handlers = append(rg.handlers, handlers...)
	return rg
}

func (rg *routeGroup) RegisterRoute(event uint32, handler Handler) *routeGroup {
	rg.router.routes[event] = append(rg.handlers, handler)
	return rg
}
