package ramix

import "github.com/gorilla/websocket"

type WebSocketConnection struct {
	*netConnection
	socket *websocket.Conn
}

func (c *WebSocketConnection) open() {
	c.netConnection.start(c, c.reader)
}

func (c *WebSocketConnection) reader() {
	for {
		messageType, buffer, err := c.socket.ReadMessage()
		if err != nil {
			if c.readCtx.Err() == nil && c.forceCtx.Err() == nil {
				c.requestClose(OperationRead, err)
			}
			return
		}

		if messageType == websocket.PingMessage || messageType == websocket.PongMessage {
			c.refreshActivity()
			continue
		}

		c.refreshActivity()
		frames, err := c.frameDecoder.Decode(buffer)
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
			_ = c.server.handleRequest(c, newRequest(message))
		}
	}
}
