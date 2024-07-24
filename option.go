package ramix

import "time"

var defaultServerOptions = ServerOptions{
	OnlyWebSocket:         false,
	OnlyTCP:               false,
	Name:                  "ramix",
	IP:                    "0.0.0.0",
	IPVersion:             "tcp4",
	Port:                  8899,
	WebSocketPort:         8900,
	WebSocketPath:         "/ws",
	MaxConnectionsCount:   1024,
	ConnectionGroupsCount: 10,
	MaxReadBufferSize:     1024,
	UseWorkerPool:         false,
	WorkersCount:          10,
	MaxTasksCount:         1024,
	HeartbeatInterval:     5 * time.Second,
	HeartbeatTimeout:      60 * time.Second,
}

type ServerOptions struct {
	OnlyWebSocket         bool
	OnlyTCP               bool
	Name                  string
	IPVersion             string
	IP                    string
	Port                  int
	WebSocketPort         int
	WebSocketPath         string
	CertFile              string
	PrivateKeyFile        string
	MaxConnectionsCount   int
	ConnectionGroupsCount int
	MaxReadBufferSize     uint32
	UseWorkerPool         bool // true: worker pool, false: new worker per request
	WorkersCount          uint32
	MaxTasksCount         uint32
	HeartbeatInterval     time.Duration
	HeartbeatTimeout      time.Duration
}

type ServerOption func(*ServerOptions)

func OnlyWebSocket() ServerOption {
	return func(o *ServerOptions) {
		o.OnlyWebSocket = true
	}
}

func OnlyTCP() ServerOption {
	return func(o *ServerOptions) {
		o.OnlyTCP = true
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

func WithMaxReadBufferSize(maxReadBufferSize uint32) ServerOption {
	return func(o *ServerOptions) {
		o.MaxReadBufferSize = maxReadBufferSize
	}
}

func UseWorkerPool() ServerOption {
	return func(o *ServerOptions) {
		o.UseWorkerPool = true
	}
}

func WithWorkersCount(workersCount uint32) ServerOption {
	return func(o *ServerOptions) {
		o.WorkersCount = workersCount
	}
}

func WithMaxTasksCount(maxTasksCount uint32) ServerOption {
	return func(o *ServerOptions) {
		o.MaxTasksCount = maxTasksCount
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
