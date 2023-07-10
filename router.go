package ramix

type HandlerInterface func(context *Context)

type router struct {
	routes map[uint32][]HandlerInterface
}

type routeGroup struct {
	router   *router
	parent   *routeGroup
	handlers []HandlerInterface
}

func (rg *routeGroup) Group() *routeGroup {
	group := &routeGroup{
		router:   rg.router,
		parent:   rg,
		handlers: rg.handlers,
	}

	return group
}

func (rg *routeGroup) Use(handlers ...HandlerInterface) *routeGroup {
	rg.handlers = append(rg.handlers, handlers...)
	return rg
}

func (rg *routeGroup) RegisterRoute(event uint32, handler HandlerInterface) *routeGroup {
	rg.router.routes[event] = append(rg.handlers, handler)
	return rg
}
