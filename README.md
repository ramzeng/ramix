# Ramix
[![Go Reference](https://pkg.go.dev/badge/github.com/ramzeng/ramix.svg)](https://pkg.go.dev/github.com/ramzeng/ramix)
![Lint](https://github.com/ramzeng/ramix/actions/workflows/golangci-lint.yml/badge.svg)

## Introduction
**English** | [简体中文](https://github.com/ramzeng/ramix/blob/main/README-CN.md)

A lightweight TCP Server framework based on Golang.
## Structure
![image](https://github.com/ramzeng/ramix/assets/38133602/f736a468-094b-4a7c-bf23-9ea956fc063a)
## Features
- [x] Message router
- [x] Route group
- [x] Route middleware
- [x] Message encoding and decoding
- [x] Message processing queue
- [x] Message read-write separation
- [x] Connection heartbeat detection
- [x] Hooks
- [x] Logger
## TODO
- [ ] Unit test
## Installation
```bash
go get -u github.com/ramzeng/ramix
```
## Quick Start
### Server side
```go
package main

import (
	"github.com/ramzeng/ramix"
	"time"
)

func main() {
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
```
### Client side
```go
package main

import (
	"fmt"
	"github.com/ramzeng/ramix"
	"net"
	"time"
)

func main() {
	socket, err := net.Dial("tcp4", "127.0.0.1:8899")

	if err != nil {
		fmt.Println("Dial error: ", err)
		return
	}

	encoder := ramix.Encoder{}
	decoder := ramix.Decoder{}

	for {
		message := ramix.Message{
			Event: 0,
			Body:  []byte("ping"),
		}

		message.BodySize = uint32(len(message.Body))

		encodedMessage, err := encoder.Encode(message)

		if err != nil {
			fmt.Println("Encode error: ", err)
			return
		}

		_, err = socket.Write(encodedMessage)

		if err != nil {
			fmt.Println("Write error: ", err)
			return
		}

		buffer := make([]byte, 1024)

		_, err = socket.Read(buffer)

		if err != nil {
			fmt.Println("Read error: ", err)
			return
		}

		message, err = decoder.Decode(buffer)

		if err != nil {
			fmt.Println("Decode error: ", err)
			return
		}

		fmt.Printf("Server message: %s\n", message.Body)

		time.Sleep(time.Second)
	}
}
```
## License
MIT
