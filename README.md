# Ramix

[![Go Reference](https://pkg.go.dev/badge/github.com/ramzeng/ramix.svg)](https://pkg.go.dev/github.com/ramzeng/ramix)
![Tests](https://github.com/ramzeng/ramix/actions/workflows/test.yml/badge.svg)
![Lint](https://github.com/ramzeng/ramix/actions/workflows/golangci-lint.yml/badge.svg)

## Introduction

**English** | [简体中文](https://github.com/ramzeng/ramix/blob/main/README-CN.md)

Ramix is a lightweight Go framework for TCP and WebSocket servers. It provides framed message routing, ordered per-connection processing, connection lifecycle hooks, heartbeat detection, TLS, and graceful shutdown.

## Features

- Message routing, route groups, and middleware
- Length-prefixed message encoding and decoding
- Ordered per-connection processing through an internal worker pool
- Independent read and write paths
- Connection heartbeat detection and lifecycle hooks
- TCP, WebSocket, and TLS support
- Malformed-frame isolation: an invalid client is disconnected without stopping other connections or the server
- Atomic startup and phased graceful shutdown

## Installation

```bash
go get github.com/ramzeng/ramix
```

## Quick Start

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ramzeng/ramix"
)

func main() {
	server, err := ramix.NewServer(
		ramix.WithTransports(ramix.TransportTCP),
		ramix.WithPort(8899),
		ramix.WithWorkerCount(4),
		ramix.WithWorkerQueueCapacity(1024),
	)
	if err != nil {
		log.Fatal(err)
	}

	if err := server.Use(ramix.Recovery(), ramix.Logger()); err != nil {
		log.Fatal(err)
	}
	if err := server.RegisterRoute(0, func(ctx *ramix.Context) {
		_ = ctx.Connection.Send(ctx, ctx.Request.Message.Event, []byte("pong"))
	}); err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
```

`NewServer` validates its options and returns `(*Server, error)`. `Run(ctx)` is synchronous: it binds every enabled transport atomically, serves until the context is canceled or a runtime failure occurs, then waits for shared shutdown to finish.

## Transports

TCP and WebSocket are enabled by default. Restrict the server explicitly with `WithTransports`:

```go
ramix.WithTransports(ramix.TransportTCP)
ramix.WithTransports(ramix.TransportWebSocket)
ramix.WithTransports(ramix.TransportTCP, ramix.TransportWebSocket)
```

The application owns signal handling. Ramix does not install process signal handlers or terminate the process.

## Shutdown

Canceling the context passed to `Run` starts graceful shutdown. Applications may also call `Shutdown(ctx)` from another goroutine. The first stop trigger owns one shared shutdown sequence; each caller's context only limits how long that caller waits and does not cancel cleanup for other callers.

Shutdown stops new intake, drains accepted handler tasks and response writes, closes connections, and waits for serving goroutines. A forced cleanup returns an error matching `ErrShutdownTimeout`. Later `Shutdown` calls observe the same terminal result.

## Sending Messages

Connections send messages with a context:

```go
err := connection.Send(ctx, event, body)
```

The context can cancel a blocked send. Inside a route handler, pass the Ramix handler context as shown in the quick-start example.

## Worker Pool

Ramix owns a fixed internal worker pool. Configure it at construction time with `WithWorkerCount` and `WithWorkerQueueCapacity`. Tasks from one connection remain ordered, while different connections can run concurrently.

## Migration

- `NewServer(...)` now returns `(*Server, error)`; handle construction errors.
- Replace removed `Serve` calls with synchronous `Run(ctx)`.
- Replace removed `SendMessage(event, body)` calls with `Send(ctx, event, body)`.
- Replace removed `OnlyTCP` and `OnlyWebSocket` options with `WithTransports(...)`.
- Replace removed `UseWorkerPool` and `NewRoundRobinWorkerPool` customization with `WithWorkerCount` and `WithWorkerQueueCapacity`.
- `Use`, `RegisterRoute`, and connection hook registration now return errors and are immutable after startup begins.

## Sponsored by JetBrains

I am very grateful to JetBrains for providing me with an open-source license for their IDE, which allows me to carry out development work on this project and other open-source projects smoothly.

[![](https://resources.jetbrains.com/storage/products/company/brand/logos/jb_beam.svg)](https://www.jetbrains.com/?from=https://github.com/ramzeng)

## License

MIT
