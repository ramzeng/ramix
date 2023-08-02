package ramix

import (
	"errors"
	"net"
	"sync"
	"time"
)

type Connection struct {
	ID uint64

	socket         *net.TCPConn
	isClosed       bool
	quitSignal     chan struct{}
	messageChannel chan []byte
	lastActiveTime time.Time

	server           *Server
	heartbeatChecker *heartbeatChecker

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
		case <-c.quitSignal:
			return
		case data := <-c.messageChannel:
			_, _ = c.socket.Write(data)
		}
	}
}

func (c *Connection) reader() {
	defer c.close()

	for {
		select {
		case <-c.quitSignal:
			return
		default:
			buffer := make([]byte, c.server.MaxReadBufferSize)

			_, err := c.socket.Read(buffer)

			if err != nil {
				return
			}

			c.RefreshLastActiveTime()

			message, err := c.server.decoder.Decode(buffer, c.server.MaxMessageSize)

			if err != nil {
				continue
			}

			c.server.handleRequest(c, newRequest(message, buffer))
		}
	}
}

func (c *Connection) close() {
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

	close(c.quitSignal)
	close(c.messageChannel)

	c.heartbeatChecker.stop()
	c.server.connectionManager.removeConnection(c)

	debug("Connection closed: %v", c.socket.RemoteAddr())
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
