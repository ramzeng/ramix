package ramix

import "testing"

func TestRouteGroup_Use(t *testing.T) {
	rg := newGroup(newRouter())

	rg.Use(func(context *Context) {
		context.Next()
	})

	if len(rg.handlers) != 1 {
		t.Error("Expected 1 handler, got", len(rg.handlers))
	}
}

func TestRouteGroup_Group(t *testing.T) {
	rg := newGroup(newRouter())

	group := rg.Group()

	if group.parent != rg {
		t.Error("Expected parent to be rg, got", group.parent)
	}
}

func TestRouteGroup_RegisterRoute(t *testing.T) {
	rg := newGroup(newRouter())

	rg.RegisterRoute(1, func(context *Context) {
		context.Next()
	})

	if len(rg.router.routes) != 1 {
		t.Error("Expected 1 route, got", len(rg.router.routes))
	}
}
