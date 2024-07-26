package main

import (
	"github.com/ramzeng/ramix"
)

func main() {
	ramix.SetMode(ramix.DebugMode)

	server := ramix.NewServer(
		ramix.WithPort(8899),
		ramix.WithCertFile("./public_certificate.pem"),
		ramix.WithPrivateKeyFile("./private_key.pem"),
	)

	server.UseWorkerPool(ramix.NewRoundRobinWorkerPool(100, 1024))
	server.Use(ramix.Recovery(), ramix.Logger())

	server.RegisterRoute(0, func(context *ramix.Context) {
		_ = context.Connection.SendMessage(context.Request.Message.Event, []byte("pong"))
	})

	server.Serve()
}
