package ramix

import (
	"fmt"
	"io"
	"os"
	"time"
)

// DefaultWriter = os.Stdout
var DefaultWriter io.Writer = os.Stdout

// LoggerConfig defines the config for Logger middleware.
type LoggerConfig struct {
	Formatter LogFormatter
	Output    io.Writer
}

// LogFormatter gives the signature of the formatter function passed to LoggerWithFormatter
type LogFormatter func(params LogFormatterParams) string

// LogFormatterParams is the structure any formatter will be handed when time to log comes
type LogFormatterParams struct {
	Connection *Connection
	Request    *Request
	TimeStamp  time.Time
	Latency    time.Duration
}

// defaultLogFormatter is the default log format function Logger middleware uses.
var defaultLogFormatter = func(param LogFormatterParams) string {
	if param.Latency > time.Minute {
		param.Latency = param.Latency.Truncate(time.Second)
	}
	return fmt.Sprintf("[ramix] %v | %14v | %10v | %2d | %4dbyte \n",
		param.TimeStamp.Format("2006/01/02 15:04:05"),
		param.Connection.socket.RemoteAddr(),
		param.Latency,
		param.Request.Message.Event,
		param.Request.Message.BodySize,
	)
}

// Logger instances a Logger middleware that will write the logs to DefaultWriter.
func Logger() HandlerInterface {
	return LoggerWithConfig(LoggerConfig{})
}

// LoggerWithFormatter instance a Logger middleware with the specified log format function.
func LoggerWithFormatter(f LogFormatter) HandlerInterface {
	return LoggerWithConfig(LoggerConfig{
		Formatter: f,
	})
}

// LoggerWithWriter instance a Logger middleware with the specified writer buffer.
func LoggerWithWriter(out io.Writer) HandlerInterface {
	return LoggerWithConfig(LoggerConfig{
		Output: out,
	})
}

// LoggerWithConfig instance a Logger middleware with config.
func LoggerWithConfig(conf LoggerConfig) HandlerInterface {
	formatter := conf.Formatter
	if formatter == nil {
		formatter = defaultLogFormatter
	}

	out := conf.Output
	if out == nil {
		// default wirter
		out = DefaultWriter
	}

	return func(c *Context) {
		// Start timer
		start := time.Now()
		c.Next()

		param := LogFormatterParams{
			Request:    c.Request,
			Connection: c.Connection,
		}
		// Stop timer
		param.TimeStamp = time.Now()
		param.Latency = param.TimeStamp.Sub(start)

		fmt.Fprint(out, formatter(param))
	}
}
