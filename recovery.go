package ramix

import (
	"fmt"
	"log"
	"runtime"
	"strings"
)

func trace(message string) string {
	var pcs [32]uintptr
	var str strings.Builder

	n := runtime.Callers(3, pcs[:]) // skip first 3 caller
	str.WriteString(message + "\nTraceback:")

	for _, pc := range pcs[:n] {
		fn := runtime.FuncForPC(pc)
		file, line := fn.FileLine(pc)
		str.WriteString(fmt.Sprintf("\n\t%s:%d", file, line))
	}

	return str.String()
}

func Recovery() HandlerInterface {
	return func(context *Context) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("%s\n\n", trace(fmt.Sprintf("%s", err)))
				// TODO: config instead of hard code
				_ = context.Connection.SendMessage(500, []byte("Server Error"))
			}
		}()

		context.Next()
	}
}
