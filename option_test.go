package ramix

import (
	"testing"
	"time"
)

func TestWithName(t *testing.T) {
	serverOptions := defaultServerOptions
	serverOption := WithName("test")
	serverOption(&serverOptions)
	if serverOptions.Name != "test" {
		t.Error("serverOptions.Name should be test")
	}
}

func TestWithIPVersion(t *testing.T) {
	serverOptions := defaultServerOptions
	serverOption := WithIPVersion("tcp6")
	serverOption(&serverOptions)
	if serverOptions.IPVersion != "tcp6" {
		t.Error("serverOptions.IPVersion should be tcp6")
	}
}

func TestWithIP(t *testing.T) {
	serverOptions := defaultServerOptions
	serverOption := WithIP("127.0.0.1")
	serverOption(&serverOptions)
	if serverOptions.IP != "127.0.0.1" {
		t.Error("serverOptions.IP should be 127.0.0.1")
	}
}

func TestWithPort(t *testing.T) {
	serverOptions := defaultServerOptions
	serverOption := WithPort(8080)
	serverOption(&serverOptions)
	if serverOptions.Port != 8080 {
		t.Error("serverOptions.Port should be 8898")
	}
}

func TestWithMaxConnectionsCount(t *testing.T) {
	serverOptions := defaultServerOptions
	serverOption := WithMaxConnectionsCount(10)
	serverOption(&serverOptions)
	if serverOptions.MaxConnectionsCount != 10 {
		t.Error("serverOptions.MaxConnectionsCount should be 10")
	}
}

func TestWithMaxReadBufferSize(t *testing.T) {
	serverOptions := defaultServerOptions
	serverOption := WithMaxReadBufferSize(2048)
	serverOption(&serverOptions)
	if serverOptions.MaxReadBufferSize != 2048 {
		t.Error("serverOptions.MaxReadBufferSize should be 2048")
	}
}

func TestWithMaxMessageSize(t *testing.T) {
	serverOptions := defaultServerOptions
	serverOption := WithMaxMessageSize(2048)
	serverOption(&serverOptions)
	if serverOptions.MaxMessageSize != 2048 {
		t.Error("serverOptions.MaxMessageSize should be 2048")
	}
}

func TestWithMaxTasksCount(t *testing.T) {
	serverOptions := defaultServerOptions
	serverOption := WithMaxTasksCount(2048)
	serverOption(&serverOptions)
	if serverOptions.MaxTasksCount != 2048 {
		t.Error("serverOptions.MaxTasksCount should be 2048")
	}
}

func TestWithWorkersCount(t *testing.T) {
	serverOptions := defaultServerOptions
	serverOption := WithWorkersCount(2048)
	serverOption(&serverOptions)
	if serverOptions.WorkersCount != 2048 {
		t.Error("serverOptions.WorkersCount should be 2048")
	}
}

func TestWithHeartbeatInterval(t *testing.T) {
	serverOptions := defaultServerOptions
	serverOption := WithHeartbeatInterval(10 * time.Second)
	serverOption(&serverOptions)
	if serverOptions.HeartbeatInterval != 10*time.Second {
		t.Error("serverOptions.HeartbeatInterval should be 2048")
	}
}

func TestWithHeartbeatTimeout(t *testing.T) {
	serverOptions := defaultServerOptions
	serverOption := WithHeartbeatTimeout(10 * time.Second)
	serverOption(&serverOptions)
	if serverOptions.HeartbeatTimeout != 10*time.Second {
		t.Error("serverOptions.HeartbeatTimeout should be 2048")
	}
}
