package ramix

import (
	"fmt"
	"strings"
	"time"
)

func IsDebugMode() bool {
	return mode == DebugMode
}

func debug(message string, contexts ...any) {
	if IsDebugMode() {
		if !strings.HasSuffix(message, "\n") {
			message += "\n"
		}

		prefix := fmt.Sprintf("[ramix-debug] %v | ", time.Now().Format("2006/01/02 15:04:05"))

		_, _ = fmt.Fprintf(DefaultWriter, prefix+message, contexts...)
	}
}
