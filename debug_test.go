package ramix

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestIsDebugMode(t *testing.T) {
	SetMode(DebugMode)

	if !IsDebugMode() {
		t.Error("IsDebugMode() should return true")
	}
}

func TestDebug(t *testing.T) {
	DefaultWriter = &bytes.Buffer{}

	SetMode(DebugMode)

	debug("Hello, %s", "ramix")

	if strings.HasSuffix(DefaultWriter.(*bytes.Buffer).String(), "Hello, ramix") {
		t.Error("debug() should write to DefaultWriter")
	}
}

func TestDebugConcurrentWrites(t *testing.T) {
	buffer := &bytes.Buffer{}
	DefaultWriter = buffer
	SetMode(DebugMode)

	var writers sync.WaitGroup
	for i := 0; i < 32; i++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			debug("Hello, %s", "ramix")
		}()
	}

	writers.Wait()

	output := buffer.String()
	if count := strings.Count(output, "Hello, ramix\n"); count != 32 {
		t.Fatalf("message count = %d, want 32", count)
	}
}
