package ramix

import "errors"

var (
	ErrInvalidConfiguration = errors.New("invalid configuration")
	ErrInvalidFrame         = errors.New("invalid frame")
	ErrFrameTooLarge        = errors.New("frame too large")
	ErrConnectionClosed     = errors.New("connection closed")
	ErrWorkerQueueFull      = errors.New("worker queue full")
	ErrServerRunning        = errors.New("server running")
	ErrServerStopping       = errors.New("server stopping")
	ErrServerStopped        = errors.New("server stopped")
	ErrShutdownTimeout      = errors.New("shutdown timeout")
)

type ConnectionOperation string

const (
	OperationRead      ConnectionOperation = "read"
	OperationWrite     ConnectionOperation = "write"
	OperationProtocol  ConnectionOperation = "protocol"
	OperationHeartbeat ConnectionOperation = "heartbeat"
	OperationTask      ConnectionOperation = "task"
	OperationOpenHook  ConnectionOperation = "open_hook"
	OperationCloseHook ConnectionOperation = "close_hook"
)

type ConnectionErrorHandler func(Connection, ConnectionOperation, error)
