package ramix

import (
	"io"
	"os"
)

const (
	DebugMode   = "debug"
	ReleaseMode = "release"
)

var mode = DebugMode

// DefaultWriter = os.Stdout
var DefaultWriter io.Writer = os.Stdout

func SetMode(value string) {
	switch value {
	case ReleaseMode:
		mode = ReleaseMode
	case DebugMode:
		mode = DebugMode
	default:
		panic("ramix mode unknown: " + value + " (available mode: debug release)")
	}
}
