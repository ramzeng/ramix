# Ramix
[![Go Reference](https://pkg.go.dev/badge/github.com/ramzeng/ramix.svg)](https://pkg.go.dev/github.com/ramzeng/ramix)
![Lint](https://github.com/ramzeng/ramix/actions/workflows/golangci-lint.yml/badge.svg)

## 介绍
**简体中文** | [English](https://github.com/ramzeng/ramix/blob/main/README.md)

一款基于 Golang 的轻量级 TCP Server 框架
## 结构
![image](https://github.com/ramzeng/ramix/assets/38133602/f736a468-094b-4a7c-bf23-9ea956fc063a)
## 能力
- [x] 消息路由
- [x] 路由分组
- [x] 路由中间件
- [x] 消息编解码
- [x] 消息处理队列
- [x] 消息读写分离
- [x] 连接心跳检测
- [x] Hooks
- [x] 日志能力
- [x] TLS 支持
- [x] WebSocket Support
## TODO
- [ ] 单元测试
## 安装
```bash
go get -u github.com/ramzeng/ramix
```
## 快速开始
### 服务端
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
	)

	server.UseWorkerPool(ramix.NewRoundRobinWorkerPool(100, 1024))
	server.Use(ramix.Recovery(), ramix.Logger())

	server.RegisterRoute(0, func(context *ramix.Context) {
		_ = context.Connection.SendMessage(context.Request.Message.Event, []byte("pong"))
	})

	server.Serve()
}
```
### 客户端
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
## 由 JetBrains 赞助

非常感谢 Jetbrains 为我提供的 IDE 开源许可，让我完成此项目和其他开源项目上的开发工作。

[![](https://resources.jetbrains.com/storage/products/company/brand/logos/jb_beam.svg)](https://www.jetbrains.com/?from=https://github.com/ramzeng)

## 协议
MIT
