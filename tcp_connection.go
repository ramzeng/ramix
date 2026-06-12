package ramix

import (
	"errors"
	"fmt"
	"io"
	"net"
)

type TCPConnection struct {
	*netConnection
	socket net.Conn
}

func (c *TCPConnection) reader() {
	buffer := make([]byte, c.server.ConnectionReadBufferSize)

	for {
		length, readErr := c.socket.Read(buffer)
		if length > 0 {
			c.refreshActivity()
			if err := c.processInput(buffer[:length]); err != nil {
				if errors.Is(err, ErrServerStopping) {
					return
				}
				c.server.reportConnectionError(c, OperationProtocol, err)
				c.requestClose(OperationProtocol, err)
				return
			}
			if c.connectionState() >= connectionClosing {
				return
			}
		}

		if readErr == nil {
			continue
		}

		if c.readCtx.Err() != nil || c.forceCtx.Err() != nil {
			return
		}

		if !errors.Is(readErr, io.EOF) && !errors.Is(readErr, net.ErrClosed) {
			c.server.reportConnectionError(c, OperationRead, readErr)
			c.requestClose(OperationRead, readErr)
			return
		}

		if c.frameDecoderHasPending() {
			err := fmt.Errorf("%w: connection ended with a partial frame", ErrInvalidFrame)
			c.server.reportConnectionError(c, OperationProtocol, err)
			c.requestClose(OperationProtocol, err)
			return
		}

		c.requestClose(OperationRead, readErr)
		return
	}
}

func (c *TCPConnection) processInput(input []byte) error {
	frames, err := c.frameDecoder.Decode(input)
	if err != nil {
		return err
	}

	for _, frame := range frames {
		message, err := c.server.decoder.Decode(frame)
		if err != nil {
			return err
		}

		err = c.server.handleRequest(c, newRequest(message))
		switch {
		case err == nil:
		case errors.Is(err, ErrServerStopping):
			return ErrServerStopping
		case errors.Is(err, ErrWorkerQueueFull):
			c.server.reportConnectionError(c, OperationTask, err)
			c.requestClose(OperationTask, err)
			return nil
		default:
			c.server.reportConnectionError(c, OperationTask, err)
			c.requestClose(OperationTask, err)
			return nil
		}
	}

	return nil
}

func (c *TCPConnection) open() {
	c.start(c, c.reader)
}

func writeFull(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if written < 0 || written > len(data) {
			return io.ErrShortWrite
		}
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrNoProgress
		}
		data = data[written:]
	}
	return nil
}
