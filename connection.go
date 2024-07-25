package ramix

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

type Connection interface {
	ID() uint64
	RemoteAddress() net.Addr
	SendMessage(event uint32, body []byte) error
	open()
	writer()
	reader()
	close(syncConnectionManager bool)
	refreshLastActiveTime()
	isAlive() bool
	pushTask(ctx *Context)
}

type netConnection struct {
	id               uint64
	isClosed         bool
	ctx              context.Context
	cancel           context.CancelFunc
	messageChannel   chan []byte
	lastActiveTime   time.Time
	server           *Server
	worker           *worker
	heartbeatChecker *heartbeatChecker
	frameDecoder     *FrameDecoder
	lock             sync.RWMutex
}

func (c *netConnection) refreshLastActiveTime() {
	c.lastActiveTime = time.Now()
}

func (c *netConnection) isAlive() bool {
	return !c.isClosed && c.lastActiveTime.Add(c.server.HeartbeatTimeout).After(time.Now())
}

func (c *netConnection) pushTask(ctx *Context) {
	c.worker.tasks <- ctx
}

func (c *netConnection) ID() uint64 {
	return c.id
}

func (c *netConnection) SendMessage(event uint32, body []byte) error {
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

func newNetConnection(connectionID uint64, s *Server) *netConnection {
	return &netConnection{
		id:             connectionID,
		isClosed:       false,
		messageChannel: make(chan []byte, s.ConnectionWriteBufferSize),
		server:         s,
		frameDecoder: NewFrameDecoder(
			WithLengthFieldOffset(4),
			WithLengthFieldLength(4),
		),
	}
}
