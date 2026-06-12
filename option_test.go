package ramix

import (
	"errors"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestDefaultServerOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want any
		got  func(ServerOptions) any
	}{
		{
			name: "default transports are tcp then websocket",
			want: []Transport{TransportTCP, TransportWebSocket},
			got: func(opts ServerOptions) any {
				return opts.Transports
			},
		},
		{
			name: "default shutdown timeout is ten seconds",
			want: 10 * time.Second,
			got: func(opts ServerOptions) any {
				return opts.ShutdownTimeout
			},
		},
		{
			name: "default worker count matches gomaxprocs",
			want: uint32(runtime.GOMAXPROCS(0)),
			got: func(opts ServerOptions) any {
				return opts.WorkerCount
			},
		},
		{
			name: "default worker queue capacity is 1024",
			want: uint32(1024),
			got: func(opts ServerOptions) any {
				return opts.WorkerQueueCapacity
			},
		},
		{
			name: "default max frame length is one mib",
			want: uint64(1 << 20),
			got: func(opts ServerOptions) any {
				return opts.MaxFrameLength
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := defaultServerOptions()
			if got := tt.got(opts); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWithTransportsCopiesAndDeduplicatesInput(t *testing.T) {
	t.Parallel()

	opts := defaultServerOptions()
	input := []Transport{TransportWebSocket, TransportTCP, TransportWebSocket, TransportTCP}

	WithTransports(input...)(&opts)

	want := []Transport{TransportWebSocket, TransportTCP}
	if !reflect.DeepEqual(opts.Transports, want) {
		t.Fatalf("transports = %v, want %v", opts.Transports, want)
	}

	input[0] = Transport(99)
	if !reflect.DeepEqual(opts.Transports, want) {
		t.Fatalf("transports mutated with caller slice: got %v, want %v", opts.Transports, want)
	}
}

func TestValidateServerOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts ServerOptions
	}{
		{
			name: "no transports",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				WithTransports()(&opts)
				return opts
			}(),
		},
		{
			name: "unknown transport",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.Transports = []Transport{Transport(99)}
				return opts
			}(),
		},
		{
			name: "tcp port below range",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.Port = -1
				return opts
			}(),
		},
		{
			name: "websocket port above range",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.WebSocketPort = 65536
				return opts
			}(),
		},
		{
			name: "empty websocket path when enabled",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.WebSocketPath = ""
				return opts
			}(),
		},
		{
			name: "non-positive max connections count",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.MaxConnectionsCount = 0
				return opts
			}(),
		},
		{
			name: "non-positive connection groups count",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.ConnectionGroupsCount = 0
				return opts
			}(),
		},
		{
			name: "zero read buffer size",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.ConnectionReadBufferSize = 0
				return opts
			}(),
		},
		{
			name: "zero write buffer size",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.ConnectionWriteBufferSize = 0
				return opts
			}(),
		},
		{
			name: "zero worker count",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.WorkerCount = 0
				return opts
			}(),
		},
		{
			name: "zero worker queue capacity",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.WorkerQueueCapacity = 0
				return opts
			}(),
		},
		{
			name: "zero max frame length",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.MaxFrameLength = 0
				return opts
			}(),
		},
		{
			name: "zero shutdown timeout",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.ShutdownTimeout = 0
				return opts
			}(),
		},
		{
			name: "negative shutdown timeout",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.ShutdownTimeout = -1 * time.Second
				return opts
			}(),
		},
		{
			name: "non-positive heartbeat interval",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.HeartbeatInterval = 0
				return opts
			}(),
		},
		{
			name: "non-positive heartbeat timeout",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.HeartbeatTimeout = 0
				return opts
			}(),
		},
		{
			name: "heartbeat timeout shorter than interval",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.HeartbeatInterval = 5 * time.Second
				opts.HeartbeatTimeout = 4 * time.Second
				return opts
			}(),
		},
		{
			name: "certificate without private key",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.CertFile = "cert.pem"
				opts.PrivateKeyFile = ""
				return opts
			}(),
		},
		{
			name: "private key without certificate",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.CertFile = ""
				opts.PrivateKeyFile = "key.pem"
				return opts
			}(),
		},
		{
			name: "invalid ip version",
			opts: func() ServerOptions {
				opts := defaultServerOptions()
				opts.IPVersion = "udp4"
				return opts
			}(),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateServerOptions(tt.opts)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("expected error to wrap ErrInvalidConfiguration, got %v", err)
			}
		})
	}
}

func TestValidateServerOptionsAllowsPortZero(t *testing.T) {
	t.Parallel()

	opts := defaultServerOptions()
	opts.Port = 0
	opts.WebSocketPort = 0

	if err := validateServerOptions(opts); err != nil {
		t.Fatalf("validateServerOptions() error = %v", err)
	}
}

func TestValidateServerOptionsAllowsInvalidIPVersionWithoutTCP(t *testing.T) {
	t.Parallel()

	opts := defaultServerOptions()
	WithTransports(TransportWebSocket)(&opts)
	opts.IPVersion = "unused-invalid-value"

	if err := validateServerOptions(opts); err != nil {
		t.Fatalf("validateServerOptions() error = %v", err)
	}
}
