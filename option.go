package ramix

import "time"

var defaultServerOptions = ServerOptions{
	Name:                "ramix",
	IP:                  "0.0.0.0",
	IPVersion:           "tcp4",
	Port:                8899,
	MaxConnectionsCount: 1024,
	MaxReadBufferSize:   1024,
	WorkersCount:        10,
	MaxTasksCount:       1024,
	HeartbeatInterval:   5 * time.Second,
	HeartbeatTimeout:    60 * time.Second,
}

type ServerOptions struct {
	Name                string
	IPVersion           string
	IP                  string
	Port                int
	CertFile            string
	PrivateKeyFile      string
	MaxConnectionsCount int
	MaxReadBufferSize   uint32
	MaxTasksCount       uint32
	WorkersCount        uint32
	HeartbeatInterval   time.Duration
	HeartbeatTimeout    time.Duration
}

type ServerOption func(*ServerOptions)

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

func WithMaxReadBufferSize(maxReadBufferSize uint32) ServerOption {
	return func(o *ServerOptions) {
		o.MaxReadBufferSize = maxReadBufferSize
	}
}

func WithMaxTasksCount(maxTasksCount uint32) ServerOption {
	return func(o *ServerOptions) {
		o.MaxTasksCount = maxTasksCount
	}
}

func WithWorkersCount(workersCount uint32) ServerOption {
	return func(o *ServerOptions) {
		o.WorkersCount = workersCount
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
