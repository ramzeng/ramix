package ramix

import (
	"context"
	"errors"
	"testing"
	"time"
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

func TestContextCancellation(t *testing.T) {
	t.Run("parent cancellation propagates", func(t *testing.T) {
		parent, cancel := context.WithCancel(context.Background())
		ctx := newContext(parent, nil, nil)

		cancel()
		waitForContextDone(t, ctx.Done())

		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("Err() = %v, want %v", ctx.Err(), context.Canceled)
		}
	})

	t.Run("cancelTask cancels the context", func(t *testing.T) {
		ctx := newContext(context.Background(), nil, nil)

		ctx.cancelTask()
		waitForContextDone(t, ctx.Done())

		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("Err() = %v, want %v", ctx.Err(), context.Canceled)
		}
	})

	t.Run("finish cancels exactly once", func(t *testing.T) {
		ctx := newContext(context.Background(), nil, nil)

		ctx.finish()
		ctx.finish()

		waitForContextDone(t, ctx.Done())

		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("Err() = %v, want %v", ctx.Err(), context.Canceled)
		}
	})

	t.Run("nil parent falls back to background", func(t *testing.T) {
		ctx := newContext(nil, nil, nil)

		select {
		case <-ctx.Done():
			t.Fatal("Done() should not be closed for a fresh background-derived context")
		default:
		}

		ctx.cancelTask()
		waitForContextDone(t, ctx.Done())
	})
}

func waitForContextDone(t *testing.T, done <-chan struct{}) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("context was not canceled")
	}
}
