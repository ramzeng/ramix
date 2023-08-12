package main

import (
	"github.com/ramzeng/ramix"
)

func main() {
	//ramix.SetMode(ramix.ReleaseMode)
	ramix.SetMode(ramix.DebugMode)

	server := ramix.NewServer(
		ramix.WithPort(8899),
		ramix.WithWorkersCount(100),
	)

	server.Use(ramix.Recovery(), ramix.Logger())

	server.RegisterRoute(0, func(context *ramix.Context) {
		_ = context.Connection.SendMessage(context.Request.Message.Event, []byte("pong"))
	})

	server.Serve()
}
