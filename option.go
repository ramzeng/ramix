package ramix

import (
	"fmt"
	"net/url"
	pathpkg "path"
	"runtime"
	"strings"
	"time"
)

type Transport uint8

const (
	TransportTCP Transport = iota + 1
	TransportWebSocket
)

func (t Transport) String() string {
	switch t {
	case TransportTCP:
		return "tcp"
	case TransportWebSocket:
		return "websocket"
	default:
		return fmt.Sprintf("Transport(%d)", t)
	}
}

type ServerOptions struct {
	Transports                []Transport
	Name                      string
	IPVersion                 string
	IP                        string
	Port                      int
	WebSocketPort             int
	WebSocketPath             string
	CertFile                  string
	PrivateKeyFile            string
	MaxConnectionsCount       int
	ConnectionGroupsCount     int
	ConnectionReadBufferSize  uint32
	ConnectionWriteBufferSize uint32
	ShutdownTimeout           time.Duration
	WorkerCount               uint32
	WorkerQueueCapacity       uint32
	MaxFrameLength            uint64
	HeartbeatInterval         time.Duration
	HeartbeatTimeout          time.Duration
}

type ServerOption func(*ServerOptions)

func defaultServerOptions() ServerOptions {
	return ServerOptions{
		Transports:                []Transport{TransportTCP, TransportWebSocket},
		Name:                      "ramix",
		IP:                        "0.0.0.0",
		IPVersion:                 "tcp4",
		Port:                      8899,
		WebSocketPort:             8900,
		WebSocketPath:             "/ws",
		MaxConnectionsCount:       1024,
		ConnectionGroupsCount:     10,
		ConnectionReadBufferSize:  1024,
		ConnectionWriteBufferSize: 1024,
		ShutdownTimeout:           10 * time.Second,
		WorkerCount:               uint32(runtime.GOMAXPROCS(0)),
		WorkerQueueCapacity:       1024,
		MaxFrameLength:            1 << 20,
		HeartbeatInterval:         5 * time.Second,
		HeartbeatTimeout:          60 * time.Second,
	}
}

func (o ServerOptions) HasTransport(transport Transport) bool {
	for _, enabled := range o.Transports {
		if enabled == transport {
			return true
		}
	}

	return false
}

func WithTransports(transports ...Transport) ServerOption {
	copied := make([]Transport, 0, len(transports))
	seen := make(map[Transport]struct{}, len(transports))

	for _, transport := range transports {
		if _, ok := seen[transport]; ok {
			continue
		}

		seen[transport] = struct{}{}
		copied = append(copied, transport)
	}

	return func(o *ServerOptions) {
		o.Transports = append([]Transport(nil), copied...)
	}
}

func WithShutdownTimeout(shutdownTimeout time.Duration) ServerOption {
	return func(o *ServerOptions) {
		o.ShutdownTimeout = shutdownTimeout
	}
}

func WithWorkerCount(workerCount uint32) ServerOption {
	return func(o *ServerOptions) {
		o.WorkerCount = workerCount
	}
}

func WithWorkerQueueCapacity(workerQueueCapacity uint32) ServerOption {
	return func(o *ServerOptions) {
		o.WorkerQueueCapacity = workerQueueCapacity
	}
}

func WithServerMaxFrameLength(maxFrameLength uint64) ServerOption {
	return func(o *ServerOptions) {
		o.MaxFrameLength = maxFrameLength
	}
}

func WithName(name string) ServerOption {
	return func(o *ServerOptions) {
		o.Name = name
	}
}

func WithIPVersion(ipVersion string) ServerOption {
	return func(o *ServerOptions) {
		o.IPVersion = ipVersion
	}
}

func WithIP(ip string) ServerOption {
	return func(o *ServerOptions) {
		o.IP = ip
	}
}

func WithPort(port int) ServerOption {
	return func(o *ServerOptions) {
		o.Port = port
	}
}

func WithWebSocketPort(webSocketPort int) ServerOption {
	return func(o *ServerOptions) {
		o.WebSocketPort = webSocketPort
	}
}

func WithWebSocketPath(webSocketPath string) ServerOption {
	return func(o *ServerOptions) {
		o.WebSocketPath = webSocketPath
	}
}

func WithCertFile(certFile string) ServerOption {
	return func(o *ServerOptions) {
		o.CertFile = certFile
	}
}

func WithPrivateKeyFile(privateKeyFile string) ServerOption {
	return func(o *ServerOptions) {
		o.PrivateKeyFile = privateKeyFile
	}
}

func WithMaxConnectionsCount(maxConnectionsCount int) ServerOption {
	return func(o *ServerOptions) {
		o.MaxConnectionsCount = maxConnectionsCount
	}
}

func WithConnectionGroupsCount(connectionGroupsCount int) ServerOption {
	return func(o *ServerOptions) {
		o.ConnectionGroupsCount = connectionGroupsCount
	}
}

func WithConnectionReadBufferSize(connectionReadBufferSize uint32) ServerOption {
	return func(o *ServerOptions) {
		o.ConnectionReadBufferSize = connectionReadBufferSize
	}
}

func WithConnectionWriteBufferSize(connectionWriteBufferSize uint32) ServerOption {
	return func(o *ServerOptions) {
		o.ConnectionWriteBufferSize = connectionWriteBufferSize
	}
}

func WithHeartbeatInterval(heartbeatInterval time.Duration) ServerOption {
	return func(o *ServerOptions) {
		o.HeartbeatInterval = heartbeatInterval
	}
}

func WithHeartbeatTimeout(heartbeatTimeout time.Duration) ServerOption {
	return func(o *ServerOptions) {
		o.HeartbeatTimeout = heartbeatTimeout
	}
}

func validateServerOptions(opts ServerOptions) error {
	if len(opts.Transports) == 0 {
		return fmt.Errorf("%w: transports must not be empty", ErrInvalidConfiguration)
	}

	seenTransports := make(map[Transport]struct{}, len(opts.Transports))
	for _, transport := range opts.Transports {
		switch transport {
		case TransportTCP, TransportWebSocket:
		default:
			return fmt.Errorf("%w: unsupported transport %q", ErrInvalidConfiguration, transport.String())
		}
		if _, exists := seenTransports[transport]; exists {
			return fmt.Errorf("%w: duplicate transport %q", ErrInvalidConfiguration, transport.String())
		}
		seenTransports[transport] = struct{}{}
	}

	if opts.HasTransport(TransportTCP) {
		if opts.Port < 0 || opts.Port > 65535 {
			return fmt.Errorf("%w: port must be between 0 and 65535: %d", ErrInvalidConfiguration, opts.Port)
		}
	}

	if opts.HasTransport(TransportWebSocket) {
		if opts.WebSocketPort < 0 || opts.WebSocketPort > 65535 {
			return fmt.Errorf("%w: websocket port must be between 0 and 65535: %d", ErrInvalidConfiguration, opts.WebSocketPort)
		}
		if opts.WebSocketPath == "" {
			return fmt.Errorf("%w: websocket path must not be empty", ErrInvalidConfiguration)
		}
		if opts.WebSocketPath[0] != '/' {
			return fmt.Errorf("%w: websocket path must start with '/': %q", ErrInvalidConfiguration, opts.WebSocketPath)
		}
		if strings.ContainsAny(opts.WebSocketPath, "{}") {
			return fmt.Errorf("%w: websocket path must not contain ServeMux wildcards: %q", ErrInvalidConfiguration, opts.WebSocketPath)
		}
		parsedPath, err := url.ParseRequestURI(opts.WebSocketPath)
		if err != nil || parsedPath.RawQuery != "" || parsedPath.Fragment != "" {
			return fmt.Errorf("%w: invalid websocket path %q", ErrInvalidConfiguration, opts.WebSocketPath)
		}
		cleanedPath := pathpkg.Clean(opts.WebSocketPath)
		if strings.HasSuffix(opts.WebSocketPath, "/") && opts.WebSocketPath != "/" {
			cleanedPath += "/"
		}
		if cleanedPath != opts.WebSocketPath {
			return fmt.Errorf("%w: websocket path must be clean: %q", ErrInvalidConfiguration, opts.WebSocketPath)
		}
	}

	if opts.MaxConnectionsCount <= 0 {
		return fmt.Errorf("%w: max connections count must be positive: %d", ErrInvalidConfiguration, opts.MaxConnectionsCount)
	}

	if opts.ConnectionGroupsCount <= 0 {
		return fmt.Errorf("%w: connection groups count must be positive: %d", ErrInvalidConfiguration, opts.ConnectionGroupsCount)
	}

	if opts.ConnectionReadBufferSize == 0 {
		return fmt.Errorf("%w: connection read buffer size must be positive", ErrInvalidConfiguration)
	}

	if opts.ConnectionWriteBufferSize == 0 {
		return fmt.Errorf("%w: connection write buffer size must be positive", ErrInvalidConfiguration)
	}

	if opts.WorkerCount == 0 {
		return fmt.Errorf("%w: worker count must be positive", ErrInvalidConfiguration)
	}

	if opts.WorkerQueueCapacity == 0 {
		return fmt.Errorf("%w: worker queue capacity must be positive", ErrInvalidConfiguration)
	}

	if opts.MaxFrameLength == 0 {
		return fmt.Errorf("%w: max frame length must be positive", ErrInvalidConfiguration)
	}

	if opts.ShutdownTimeout <= 0 {
		return fmt.Errorf("%w: shutdown timeout must be positive: %s", ErrInvalidConfiguration, opts.ShutdownTimeout)
	}

	if opts.HeartbeatInterval <= 0 {
		return fmt.Errorf("%w: heartbeat interval must be positive: %s", ErrInvalidConfiguration, opts.HeartbeatInterval)
	}

	if opts.HeartbeatTimeout <= 0 {
		return fmt.Errorf("%w: heartbeat timeout must be positive: %s", ErrInvalidConfiguration, opts.HeartbeatTimeout)
	}

	if opts.HeartbeatTimeout < opts.HeartbeatInterval {
		return fmt.Errorf("%w: heartbeat timeout must be greater than or equal to heartbeat interval: timeout=%s interval=%s", ErrInvalidConfiguration, opts.HeartbeatTimeout, opts.HeartbeatInterval)
	}

	if (opts.CertFile == "") != (opts.PrivateKeyFile == "") {
		return fmt.Errorf("%w: cert file and private key file must be provided together", ErrInvalidConfiguration)
	}

	if opts.HasTransport(TransportTCP) {
		switch opts.IPVersion {
		case "tcp", "tcp4", "tcp6":
		default:
			return fmt.Errorf("%w: invalid ip version %q", ErrInvalidConfiguration, opts.IPVersion)
		}
	}

	return nil
}
