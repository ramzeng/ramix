package ramix

import (
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/gorilla/websocket"
)

const webSocketControlWriteTimeout = 5 * time.Second

type WebSocketConnection struct {
	*netConnection
	socket *websocket.Conn
}

func (c *WebSocketConnection) open() {
	c.installControlHandlers()
	c.netConnection.start(c, c.reader)
}

func (c *WebSocketConnection) installControlHandlers() {
	c.socket.SetPingHandler(func(applicationData string) error {
		c.refreshActivity()
		err := c.socket.WriteControl(
			websocket.PongMessage,
			[]byte(applicationData),
			time.Now().Add(webSocketControlWriteTimeout),
		)
		if err != nil && c.tryRequestClose(OperationWrite, err) {
			c.server.reportConnectionError(c, OperationWrite, err)
		}
		return err
	})
	c.socket.SetPongHandler(func(string) error {
		c.refreshActivity()
		return nil
	})
}

func (c *WebSocketConnection) reader() {
	for {
		messageType, buffer, err := c.socket.ReadMessage()
		if err != nil {
			if c.readCtx.Err() != nil || c.forceCtx.Err() != nil {
				return
			}
			if errors.Is(err, net.ErrClosed) || websocket.IsCloseError(
				err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
			) {
				c.requestClose(OperationRead, err)
				return
			}
			if c.tryRequestClose(OperationRead, err) {
				c.server.reportConnectionError(c, OperationRead, err)
			}
			return
		}

		if messageType != websocket.BinaryMessage {
			c.fail(OperationProtocol, fmt.Errorf(
				"%w: websocket message type %d is not binary",
				ErrInvalidFrame,
				messageType,
			))
			return
		}
		if len(buffer) == 0 {
			c.fail(OperationProtocol, fmt.Errorf(
				"%w: websocket binary message is empty",
				ErrInvalidFrame,
			))
			return
		}

		c.refreshActivity()
		frames, err := c.frameDecoder.Decode(buffer)
		if err != nil {
			c.fail(OperationProtocol, err)
			return
		}
		if c.frameDecoderHasPending() {
			c.fail(OperationProtocol, fmt.Errorf(
				"%w: websocket binary message ended with a partial frame",
				ErrInvalidFrame,
			))
			return
		}

		for _, frame := range frames {
			message, err := c.server.decoder.Decode(frame)
			if err != nil {
				c.fail(OperationProtocol, err)
				return
			}

			err = c.server.handleRequest(c, newRequest(message))
			switch {
			case err == nil:
			case errors.Is(err, ErrServerStopping):
				return
			default:
				c.fail(OperationTask, err)
				return
			}
		}
	}
}

func (c *WebSocketConnection) fail(operation ConnectionOperation, err error) {
	if c.tryRequestClose(operation, err) {
		c.server.reportConnectionError(c, operation, err)
	}
}
