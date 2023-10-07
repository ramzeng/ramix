package ramix

import "testing"

func TestNewServer(t *testing.T) {
	server := NewServer()

	if server == nil {
		t.Error("NewServer() should not return nil")
	}
}
