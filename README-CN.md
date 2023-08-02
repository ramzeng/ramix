# Ramix
## 介绍
**简体中文** | [English](https://github.com/ranpro/ramix/blob/main/README.md)

一款基于 Golang 的轻量级 TCP Server 框架
## 结构
![image](https://github.com/ranpro/ramix/assets/38133602/f736a468-094b-4a7c-bf23-9ea956fc063a)
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
## TODO
- [ ] 单元测试
- [ ] WorkerPool
## 安装
```bash
go get -u github.com/ranpro/ramix
```
## 快速开始
### 服务端
```go
package main

import (
	"github.com/ranpro/ramix"
	"time"
)

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
	"github.com/ranpro/ramix"
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

		message, err = decoder.Decode(buffer, 1024)

		if err != nil {
			fmt.Println("Decode error: ", err)
			return
		}

		fmt.Printf("Server message: %s\n", message.Body)

		time.Sleep(time.Second)
	}
}
```
## 协议
MIT
