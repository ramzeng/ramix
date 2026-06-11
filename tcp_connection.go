package ramix

import "net"

type TCPConnection struct {
	*netConnection
	socket net.Conn
}

func (c *TCPConnection) reader() {
	for {
		buffer := make([]byte, c.server.ConnectionReadBufferSize)
		length, err := c.socket.Read(buffer)
		if err != nil {
			if c.readCtx.Err() == nil && c.forceCtx.Err() == nil {
				c.requestClose(OperationRead, err)
			}
			return
		}

		c.refreshActivity()
		frames, err := c.frameDecoder.Decode(buffer[:length])
		if err != nil {
			c.requestClose(OperationProtocol, err)
			return
		}

		for _, frame := range frames {
			message, err := c.server.decoder.Decode(frame)
			if err != nil {
				c.requestClose(OperationProtocol, err)
				return
			}
			c.server.handleRequest(c, newRequest(message))
		}
	}
}

func (c *TCPConnection) open() {
	c.netConnection.start(c, c.reader)
}
