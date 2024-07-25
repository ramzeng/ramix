package ramix

import (
	"net"
)

type TCPConnection struct {
	*netConnection
	socket net.Conn
}

func (c *TCPConnection) writer() {
	for {
		select {
		case <-c.ctx.Done():
			debug("TCPConnection %d writer stopped", c.ID())
			return
		case data := <-c.messageChannel:
			_, _ = c.socket.Write(data)
		}
	}
}

func (c *TCPConnection) reader() {
	defer c.close(true)

	for {
		select {
		case <-c.ctx.Done():
			debug("TCPConnection %d reader stopped", c.ID())
			return
		default:
			buffer := make([]byte, c.server.ConnectionReadBufferSize)

			length, err := c.socket.Read(buffer)

			if err != nil {
				debug("TCPSocket read error: %v", err)
				return
			}

			c.refreshLastActiveTime()

			bytesSlices := c.frameDecoder.Decode(buffer[0:length])

			for _, bytesSlice := range bytesSlices {

				message, err := c.server.decoder.Decode(bytesSlice)

				if err != nil {
					debug("Message decode error: %v", err)
					continue
				}

				c.server.handleRequest(c, newRequest(message))
			}
		}
	}
}

func (c *TCPConnection) RemoteAddress() net.Addr {
	return c.socket.RemoteAddr()
}

func (c *TCPConnection) open() {
	go c.reader()
	go c.writer()
	go c.heartbeatChecker.start()

	if c.server.connectionOpen != nil {
		c.server.connectionOpen(c)
	}
}

func (c *TCPConnection) close(syncConnectionManager bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if c.isClosed {
		return
	}

	if c.server.connectionClose != nil {
		c.server.connectionClose(c)
	}

	_ = c.socket.Close()

	c.isClosed = true

	c.cancel()
	close(c.messageChannel)

	c.heartbeatChecker.stop()

	if syncConnectionManager {
		c.server.connectionManager.removeConnection(c)
	}

	// If the worker pool is not used, need to stop the worker by self
	if !c.server.UseWorkerPool {
		c.worker.stop()
	}

	debug("TCPConnection %d closed, remote address: %v", c.ID(), c.socket.RemoteAddr())
}
