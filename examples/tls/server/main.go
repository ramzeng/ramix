package main

import (
	"context"
	"github.com/ramzeng/ramix"
	"log"
	"os"
	"os/signal"
	"syscall"
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

	if err := server.Use(ramix.Recovery(), ramix.Logger()); err != nil {
		log.Fatal(err)
	}

	if err := server.RegisterRoute(0, func(context *ramix.Context) {
		_ = context.Connection.Send(context, context.Request.Message.Event, []byte("pong"))
	}); err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
