package ramix

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestNewServer(t *testing.T) {
	t.Parallel()

	server, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	if server == nil {
		t.Fatal("NewServer() returned nil server")
	}
}

func TestNewServerRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	server, err := NewServer(WithWorkerCount(0))
	if server != nil {
		t.Fatalf("NewServer() server = %v, want nil", server)
	}
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("NewServer() error = %v, want ErrInvalidConfiguration", err)
	}
}

func TestNewServerStoresExplicitOptions(t *testing.T) {
	t.Parallel()

	server, err := NewServer(
		WithTransports(TransportWebSocket),
		WithShutdownTimeout(3*time.Second),
		WithWorkerCount(2),
		WithWorkerQueueCapacity(7),
		WithServerMaxFrameLength(2048),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	if got, want := server.ShutdownTimeout, 3*time.Second; got != want {
		t.Fatalf("ShutdownTimeout = %s, want %s", got, want)
	}
	if got, want := server.WorkerCount, uint32(2); got != want {
		t.Fatalf("WorkerCount = %d, want %d", got, want)
	}
	if got, want := server.WorkerQueueCapacity, uint32(7); got != want {
		t.Fatalf("WorkerQueueCapacity = %d, want %d", got, want)
	}
	if got, want := server.MaxFrameLength, uint64(2048); got != want {
		t.Fatalf("MaxFrameLength = %d, want %d", got, want)
	}
	if got, want := server.Transports, []Transport{TransportWebSocket}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Transports = %v, want %v", got, want)
	}
}
