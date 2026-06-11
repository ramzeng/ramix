package main

import (
	"github.com/ramzeng/ramix"
	"log"
)

func main() {
	ramix.SetMode(ramix.DebugMode)

	server, err := ramix.NewServer(
		ramix.WithPort(8899),
		ramix.WithCertFile("examples/tls/public_certificate.pem"),
		ramix.WithPrivateKeyFile("examples/tls/private_key.pem"),
	)
	if err != nil {
		log.Fatal(err)
	}

	server.Use(ramix.Recovery(), ramix.Logger())

	server.RegisterRoute(0, func(context *ramix.Context) {
		_ = context.Connection.SendMessage(context.Request.Message.Event, []byte("pong"))
	})

	server.Serve()
}
