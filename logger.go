package ramix

import (
	"fmt"
	"io"
	"time"
)

// defaultLogFormatter is the default log format function Logger middleware uses.
var defaultLogFormatter = func(parameters LogFormatterParameters) string {
	if parameters.Latency > time.Minute {
		parameters.Latency = parameters.Latency.Truncate(time.Second)
	}

	return fmt.Sprintf("[ramix] %v | %14v | %10v | %d | %d byte \n",
		parameters.TimeStamp.Format("2006/01/02 15:04:05"),
		parameters.Connection.socket.RemoteAddr(),
		parameters.Latency,
		parameters.Request.Message.Event,
		parameters.Request.Message.BodySize,
	)
}

// LoggerConfig defines the config for Logger middleware.
type LoggerConfig struct {
	Formatter LogFormatter
	Writer    io.Writer
}

// LogFormatter gives the signature of the formatter function passed to LoggerWithFormatter
type LogFormatter func(params LogFormatterParameters) string

// LogFormatterParameters is the structure any formatter will be handed when time to log comes
type LogFormatterParameters struct {
	Connection *Connection
	Request    *Request
	TimeStamp  time.Time
	Latency    time.Duration
}

// Logger instances a Logger middleware that will write the logs to DefaultWriter.
func Logger() Handler {
	return LoggerWithConfig(LoggerConfig{})
}

// LoggerWithFormatter instance a Logger middleware with the specified log format function.
func LoggerWithFormatter(formatter LogFormatter) Handler {
	return LoggerWithConfig(LoggerConfig{
		Formatter: formatter,
	})
}

// LoggerWithWriter instance a Logger middleware with the specified writer buffer.
func LoggerWithWriter(writer io.Writer) Handler {
	return LoggerWithConfig(LoggerConfig{
		Writer: writer,
	})
}

// LoggerWithConfig instance a Logger middleware with config.
func LoggerWithConfig(config LoggerConfig) Handler {
	formatter := config.Formatter

	if formatter == nil {
		formatter = defaultLogFormatter
	}

	writer := config.Writer

	if writer == nil {
		// default writer is os.Stdout
		writer = DefaultWriter
	}

	return func(c *Context) {
		// start timer
		start := time.Now()

		c.Next()

		parameters := LogFormatterParameters{
			Request:    c.Request,
			Connection: c.Connection,
		}

		// stop timer
		parameters.TimeStamp = time.Now()
		parameters.Latency = parameters.TimeStamp.Sub(start)

		_, _ = fmt.Fprint(writer, formatter(parameters))
	}
}
