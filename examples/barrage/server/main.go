package main

import (
	"context"
	"github.com/ramzeng/ramix"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var (
	connections   = make(map[uint64]ramix.Connection)
	connectionsMu sync.RWMutex
)

func main() {
	ramix.SetMode(ramix.DebugMode)

	server, err := ramix.NewServer(
		ramix.WithPort(8899),
	)
	if err != nil {
		log.Fatal(err)
	}

	if err := server.Use(ramix.Recovery(), ramix.Logger()); err != nil {
		log.Fatal(err)
	}

	if err := server.OnConnectionOpen(func(connection ramix.Connection) {
		connectionsMu.Lock()
		defer connectionsMu.Unlock()
		connections[connection.ID()] = connection
	}); err != nil {
		log.Fatal(err)
	}

	if err := server.OnConnectionClose(func(connection ramix.Connection) {
		connectionsMu.Lock()
		defer connectionsMu.Unlock()
		delete(connections, connection.ID())
	}); err != nil {
		log.Fatal(err)
	}

	if err := server.RegisterRoute(0, func(context *ramix.Context) {
		connectionsMu.RLock()
		peers := make([]ramix.Connection, 0, len(connections))
		for connectionId, connection := range connections {
			if connectionId == context.Connection.ID() {
				continue
			}
			peers = append(peers, connection)
		}
		connectionsMu.RUnlock()

		for _, connection := range peers {
			_ = connection.Send(context, 0, context.Request.Message.Body)
		}
	}); err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
