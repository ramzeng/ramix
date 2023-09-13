package ramix

import (
	"github.com/gorilla/websocket"
	"net"
)

type WebSocketConnection struct {
	*netConnection
	socket *websocket.Conn
}

func (c *WebSocketConnection) open() {
	go c.reader()
	go c.writer()
	go c.heartbeatChecker.start()

	if c.server.connectionOpen != nil {
		c.server.connectionOpen(c)
	}
}

func (c *WebSocketConnection) close(syncConnectionManager bool) {
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

	debug("WebSocketConnection %d closed, remote address: %v", c.ID(), c.socket.RemoteAddr())
}

func (c *WebSocketConnection) writer() {
	for {
		select {
		case <-c.ctx.Done():
			debug("WebSocketConnection %d writer stopped", c.ID())
			return
		case data := <-c.messageChannel:
			_ = c.socket.WriteMessage(websocket.BinaryMessage, data)
		}
	}
}

func (c *WebSocketConnection) reader() {
	defer c.close(true)

	for {
		select {
		case <-c.ctx.Done():
			debug("WebSocketConnection %d reader stopped", c.ID())
			return
		default:
			messageType, buffer, err := c.socket.ReadMessage()

			if messageType == websocket.PingMessage {
				c.refreshLastActiveTime()
				continue
			}

			if err != nil {
				debug("WebSocket read error: %v", err)
				return
			}

			c.refreshLastActiveTime()

			bytesSlices := c.frameDecoder.Decode(buffer)

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

func (c *WebSocketConnection) RemoteAddress() net.Addr {
	return c.socket.RemoteAddr()
}
