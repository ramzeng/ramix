package ramix

import (
	"testing"
)

func TestContext_Get(t *testing.T) {
	c := &Context{}
	c.Set("foo", "bar")

	if c.Get("foo") != "bar" {
		t.Error("Expected 'bar', got", c.Get("foo"))
	}
}

func TestContext_Next(t *testing.T) {
	c := &Context{
		step: -1,
	}

	c.handlers = []Handler{
		func(c *Context) {
			c.Set("foo", "bar")
		},
		func(c *Context) {
			c.Set("bar", "baz")
		},
	}

	c.Next()

	if c.Get("foo") != "bar" {
		t.Error("Expected 'bar', got", c.Get("foo"))
	}

	if c.Get("bar") != "baz" {
		t.Error("Expected 'baz', got", c.Get("bar"))
	}
}

func TestContext_Set(t *testing.T) {
	c := &Context{}
	c.Set("foo", "bar")

	if c.Get("foo") != "bar" {
		t.Error("Expected 'bar', got", c.Get("foo"))
	}
}
