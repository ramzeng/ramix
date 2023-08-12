package main

import (
	"github.com/ramzeng/ramix"
)

var connections = make(map[uint64]*ramix.Connection)

func main() {
	//ramix.SetMode(ramix.ReleaseMode)
	ramix.SetMode(ramix.DebugMode)

	server := ramix.NewServer(
		ramix.WithPort(8899),
		ramix.WithWorkersCount(100),
	)

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
