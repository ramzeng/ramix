package ramix

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

type Connection struct {
	ID uint64

	socket         *net.TCPConn
	isClosed       bool
	ctx            context.Context
	cancel         context.CancelFunc
	messageChannel chan []byte
	lastActiveTime time.Time

	server           *Server
	heartbeatChecker *heartbeatChecker

	frameDecoder *FrameDecoder

	lock sync.RWMutex
}

func (c *Connection) open() {
	go c.reader()
	go c.writer()
	go c.heartbeatChecker.start()

	if c.server.connectionOpen != nil {
		c.server.connectionOpen(c)
	}
}

func (c *Connection) writer() {
	for {
		select {
		case <-c.ctx.Done():
			debug("Connection %d writer stopped", c.ID)
			return
		case data := <-c.messageChannel:
			_, _ = c.socket.Write(data)
		}
	}
}

func (c *Connection) reader() {
	defer c.close(true)

	for {
		select {
		case <-c.ctx.Done():
			debug("Connection %d reader stopped", c.ID)
			return
		default:
			buffer := make([]byte, c.server.MaxReadBufferSize)

			length, err := c.socket.Read(buffer)

			if err != nil {
				debug("Socket read error: %v", err)
				return
			}

			c.RefreshLastActiveTime()

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

func (c *Connection) close(syncManager bool) {
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

	if syncManager {
		c.server.connectionManager.removeConnection(c)
	}

	debug("Connection %d closed, remote address: %v", c.ID, c.socket.RemoteAddr())
}

func (c *Connection) SendMessage(event uint32, body []byte) error {
	if c.isClosed {
		return errors.New("connection is closed")
	}

	encodedMessage, err := c.server.encoder.Encode(Message{
		Event:    event,
		Body:     body,
		BodySize: uint32(len(body)),
	})

	if err != nil {
		return err
	}

	c.messageChannel <- encodedMessage

	return nil
}

func (c *Connection) RefreshLastActiveTime() {
	c.lastActiveTime = time.Now()
}

func (c *Connection) isAlive() bool {
	return !c.isClosed && c.lastActiveTime.Add(c.server.HeartbeatTimeout).After(time.Now())
}
