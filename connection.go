package ramix

import (
	"errors"
	"net"
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
}

func (c *Connection) open() {
	c.lastActiveTime = time.Now()
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

			c.lastActiveTime = time.Now()

			message, err := c.server.decoder.Decode(buffer, c.server.MaxMessageSize)

			if err != nil {
				continue
			}

			c.server.handleRequest(c, newRequest(message, buffer))
		}
	}
}

func (c *Connection) close() {
	if c.isClosed {
		return
	}

	if c.server.connectionClose != nil {
		c.server.connectionClose(c)
	}

	_ = c.socket.Close()

	c.isClosed = true
	c.quitSignal <- struct{}{}

	close(c.quitSignal)
	close(c.messageChannel)

	c.heartbeatChecker.stop()
	c.server.connectionManager.RemoveConnection(c)
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

func (c *Connection) isAlive() bool {
	// fmt.Printf("lastActiveTime: %v, interval: %v, after add: %v, now: %v\n", c.lastActiveTime, c.server.HeartbeatInterval, c.lastActiveTime.Add(c.server.HeartbeatInterval), time.Now())
	return !c.isClosed && c.lastActiveTime.Add(c.server.HeartbeatTimeout).After(time.Now())
}
