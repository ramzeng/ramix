package main

import (
	"github.com/ramzeng/ramix"
	"log"
	"sync"
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

	server.Use(ramix.Recovery(), ramix.Logger())

	server.OnConnectionOpen(func(connection ramix.Connection) {
		connectionsMu.Lock()
		defer connectionsMu.Unlock()
		connections[connection.ID()] = connection
	})

	server.OnConnectionClose(func(connection ramix.Connection) {
		connectionsMu.Lock()
		defer connectionsMu.Unlock()
		delete(connections, connection.ID())
	})

	server.RegisterRoute(0, func(context *ramix.Context) {
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
	})

	server.Serve()
}
