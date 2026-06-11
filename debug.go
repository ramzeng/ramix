package ramix

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

var debugWriteMu sync.Mutex

func IsDebugMode() bool {
	return mode == DebugMode
}

func debug(message string, contexts ...any) {
	if IsDebugMode() {
		if !strings.HasSuffix(message, "\n") {
			message += "\n"
		}

		prefix := fmt.Sprintf("[ramix-debug] %v | ", time.Now().Format("2006/01/02 15:04:05"))

		debugWriteMu.Lock()
		_, _ = fmt.Fprintf(DefaultWriter, prefix+message, contexts...)
		debugWriteMu.Unlock()
	}
}
