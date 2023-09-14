package ramix

import (
	"bytes"
	"strings"
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
