# Ramix

[![Go Reference](https://pkg.go.dev/badge/github.com/ramzeng/ramix.svg)](https://pkg.go.dev/github.com/ramzeng/ramix)
![Tests](https://github.com/ramzeng/ramix/actions/workflows/test.yml/badge.svg)
![Lint](https://github.com/ramzeng/ramix/actions/workflows/golangci-lint.yml/badge.svg)

## 介绍

**简体中文** | [English](https://github.com/ramzeng/ramix/blob/main/README.md)

Ramix 是一个轻量级的 Go TCP 和 WebSocket 服务端框架，提供分帧消息路由、单连接有序处理、连接生命周期钩子、心跳检测、TLS 和优雅关闭能力。

## 功能

- 消息路由、路由分组和中间件
- 基于长度字段的消息编解码
- 通过内部工作池实现单连接有序处理
- 独立的读写路径
- 连接心跳检测和生命周期钩子
- 支持 TCP、WebSocket 和 TLS
- 畸形帧隔离：无效客户端会被断开，但不会影响其他连接或停止服务端
- 原子启动和分阶段优雅关闭

## 安装

```bash
go get github.com/ramzeng/ramix
```

## 快速开始

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ramzeng/ramix"
)

func main() {
	server, err := ramix.NewServer(
		ramix.WithTransports(ramix.TransportTCP),
		ramix.WithPort(8899),
		ramix.WithWorkerCount(4),
		ramix.WithWorkerQueueCapacity(1024),
	)
	if err != nil {
		log.Fatal(err)
	}

	if err := server.Use(ramix.Recovery(), ramix.Logger()); err != nil {
		log.Fatal(err)
	}
	if err := server.RegisterRoute(0, func(ctx *ramix.Context) {
		_ = ctx.Connection.Send(ctx, ctx.Request.Message.Event, []byte("pong"))
	}); err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
```

`NewServer` 会校验配置并返回 `(*Server, error)`。`Run(ctx)` 是同步方法：它会原子绑定全部已启用的传输，在上下文被取消或出现运行时错误前持续提供服务，然后等待共享关闭流程完成。

## 传输

默认同时启用 TCP 和 WebSocket。可以使用 `WithTransports` 明确限制传输类型：

```go
ramix.WithTransports(ramix.TransportTCP)
ramix.WithTransports(ramix.TransportWebSocket)
ramix.WithTransports(ramix.TransportTCP, ramix.TransportWebSocket)
```

应用负责处理进程信号。Ramix 不会安装进程信号处理器，也不会主动终止进程。

## 关闭

取消传给 `Run` 的上下文会启动优雅关闭。应用也可以从另一个 goroutine 调用 `Shutdown(ctx)`。第一个停止触发器会启动唯一的共享关闭流程；每个调用方的上下文只限制该调用方的等待时间，不会取消其他调用方正在等待的清理流程。

关闭流程会停止接收新请求，排空已经接受的处理任务和响应写入，关闭连接，并等待传输服务 goroutine 退出。强制清理会返回匹配 `ErrShutdownTimeout` 的错误。后续 `Shutdown` 调用会观察到相同的最终结果。

## 发送消息

连接通过带上下文的方法发送消息：

```go
err := connection.Send(ctx, event, body)
```

上下文可以取消被阻塞的发送操作。在路由处理器中，请像快速开始示例一样传入 Ramix 处理器上下文。

## 工作池

Ramix 内部管理固定的工作池。使用 `WithWorkerCount` 和 `WithWorkerQueueCapacity` 在构造时配置工作池。同一连接的任务保持有序，不同连接的任务可以并发执行。

## 迁移

- `NewServer(...)` 现在返回 `(*Server, error)`，调用方必须处理构造错误。
- 使用同步的 `Run(ctx)` 替换已移除的 `Serve` 调用。
- 使用 `Send(ctx, event, body)` 替换已移除的 `SendMessage(event, body)` 调用。
- 使用 `WithTransports(...)` 替换已移除的 `OnlyTCP` 和 `OnlyWebSocket` 选项。
- 使用 `WithWorkerCount` 和 `WithWorkerQueueCapacity` 替换已移除的 `UseWorkerPool` 和 `NewRoundRobinWorkerPool` 自定义工作池方式。
- `Use`、`RegisterRoute` 和连接钩子注册方法现在会返回错误，并且在启动开始后不可再修改。

## 由 JetBrains 赞助

非常感谢 JetBrains 为我提供的 IDE 开源许可，让我完成此项目和其他开源项目上的开发工作。

[![](https://resources.jetbrains.com/storage/products/company/brand/logos/jb_beam.svg)](https://www.jetbrains.com/?from=https://github.com/ramzeng)

## 许可证

MIT
