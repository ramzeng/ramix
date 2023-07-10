package main

import (
	"github.com/ranpro/ramix"
	"time"
)

func main() {
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

	server.RegisterRoute(0, func(context *ramix.Context) {
		_ = context.Connection.SendMessage(context.Request.Message.Event, []byte("pong"))
	})

	server.Serve()
}
