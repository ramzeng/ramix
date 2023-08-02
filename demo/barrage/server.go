package main

import (
	"github.com/ranpro/ramix"
	"time"
)

var connections = make(map[uint64]*ramix.Connection)

func main() {
	//ramix.SetMode(ramix.ReleaseMode)
	ramix.SetMode(ramix.DebugMode)

	server := ramix.NewServer(ramix.ServerConfig{
		Name:                "ramix",
		IP:                  "0.0.0.0",
		IPVersion:           "tcp4",
		Port:                8899,
		MaxConnectionsCount: 3,
		MaxMessageSize:      1024,
		MaxReadBufferSize:   1024,
		WorkersCount:        10,
		MaxTasksCount:       1024,
		HeartbeatInterval:   5 * time.Second,
		HeartbeatTimeout:    60 * time.Second,
	})

	server.Use(ramix.Recovery(), ramix.Logger())

	server.OnConnectionOpen(func(connection *ramix.Connection) {
		connections[connection.ID] = connection
	})

	server.OnConnectionClose(func(connection *ramix.Connection) {
		delete(connections, connection.ID)
	})

	server.RegisterRoute(0, func(context *ramix.Context) {
		for connectionId, connection := range connections {
			if connectionId == context.Connection.ID {
				continue
			}

			_ = connection.SendMessage(0, context.Request.Message.Body)
		}
	})

	server.Serve()
}
