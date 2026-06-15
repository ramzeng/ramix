package ramix

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Connection interface {
	ID() uint64
	RemoteAddress() net.Addr
	Send(context.Context, uint32, []byte) error
}

type connectionState uint32

const (
	connectionOpen connectionState = iota
	connectionDraining
	connectionClosing
	connectionClosed
)

type connectionTransport interface {
	Close() error
	RemoteAddr() net.Addr
	SetReadDeadline(time.Time) error
}

type managedConnection interface {
	Connection
	quiesceReads() error
	stopSendsAndDrain(context.Context) error
	requestClose(ConnectionOperation, error)
	wait(context.Context) error
}

type netConnection struct {
	id              uint64
	server          *Server
	metricTransport Transport
	transport       connectionTransport
	writeMessage    func([]byte) error
	frameDecoder    *FrameDecoder
	activity        *activityClock

	state   atomic.Uint32
	stateMu sync.Mutex

	readCtx     context.Context
	readCancel  context.CancelFunc
	sendCtx     context.Context
	sendCancel  context.CancelFunc
	forceCtx    context.Context
	forceCancel context.CancelFunc

	sendMu         sync.Mutex
	acceptingSends bool
	sendWG         sync.WaitGroup
	outgoing       chan []byte
	drainWriter    chan struct{}
	sendStopOnce   sync.Once

	children   sync.WaitGroup
	writerDone chan struct{}
	done       chan struct{}
	started    atomic.Bool

	startOnce          sync.Once
	quiesceOnce        sync.Once
	quiesceErr         error
	closeOnce          sync.Once
	transportCloseOnce sync.Once
	finalizeOnce       sync.Once

	closeReasonMu sync.Mutex
	closeOp       ConnectionOperation
	closeErr      error
	self          managedConnection
}

func newNetConnection(
	connectionID uint64,
	server *Server,
	metricTransport Transport,
	transport connectionTransport,
	writeMessage func([]byte) error,
) (*netConnection, error) {
	frameDecoder, err := NewFrameDecoder(
		WithLengthFieldOffset(4),
		WithLengthFieldLength(4),
		WithMaxFrameLength(server.MaxFrameLength),
	)
	if err != nil {
		return nil, err
	}

	readCtx, readCancel := context.WithCancel(context.Background())
	sendCtx, sendCancel := context.WithCancel(context.Background())
	forceCtx, forceCancel := context.WithCancel(context.Background())

	connection := &netConnection{
		id:              connectionID,
		server:          server,
		metricTransport: metricTransport,
		transport:       transport,
		writeMessage:    writeMessage,
		frameDecoder:    frameDecoder,
		activity:        newActivityClock(time.Now),
		readCtx:         readCtx,
		readCancel:      readCancel,
		sendCtx:         sendCtx,
		sendCancel:      sendCancel,
		forceCtx:        forceCtx,
		forceCancel:     forceCancel,
		acceptingSends:  true,
		outgoing:        make(chan []byte, server.ConnectionWriteBufferSize),
		drainWriter:     make(chan struct{}),
		writerDone:      make(chan struct{}),
		done:            make(chan struct{}),
	}
	connection.state.Store(uint32(connectionOpen))
	connection.activity.refresh()

	return connection, nil
}

func (c *netConnection) ID() uint64 {
	return c.id
}

func (c *netConnection) RemoteAddress() net.Addr {
	if c.transport == nil {
		return nil
	}
	return c.transport.RemoteAddr()
}

func (c *netConnection) statsTransport() Transport {
	return c.metricTransport
}

func (c *netConnection) taskContext() context.Context {
	return c.forceCtx
}

func (c *netConnection) Send(ctx context.Context, event uint32, body []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}

	encodedMessage, err := c.server.encoder.Encode(Message{
		Event:    event,
		Body:     body,
		BodySize: uint32(len(body)),
	})
	if err != nil {
		return err
	}

	c.sendMu.Lock()
	if !c.acceptingSends || c.connectionState() >= connectionClosing {
		c.sendMu.Unlock()
		return ErrConnectionClosed
	}
	c.sendWG.Add(1)
	c.sendMu.Unlock()
	defer c.sendWG.Done()

	select {
	case c.outgoing <- encodedMessage:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.sendCtx.Done():
		return ErrConnectionClosed
	case <-c.forceCtx.Done():
		return ErrConnectionClosed
	}
}

func (c *netConnection) start(self managedConnection, reader func()) {
	c.startOnce.Do(func() {
		c.self = self
		c.server.metrics.connectionOpened(c.statsTransport())
		c.started.Store(true)
		c.children.Add(3)
		openHookDone := make(chan struct{})

		go func() {
			defer c.children.Done()
			c.runWriter()
		}()
		go func() {
			defer c.children.Done()
			reader()
		}()
		go func() {
			defer c.children.Done()
			c.runHeartbeat()
		}()
		go c.supervise(openHookDone)

		c.server.invokeOpenHook(self)
		close(openHookDone)
	})
}

func (c *netConnection) runWriter() {
	defer close(c.writerDone)

	for {
		select {
		case <-c.forceCtx.Done():
			return
		default:
		}

		select {
		case <-c.forceCtx.Done():
			return
		case data := <-c.outgoing:
			if err := c.writeOutgoing(data); err != nil {
				if c.tryRequestClose(OperationWrite, err) {
					c.server.reportConnectionError(c.self, OperationWrite, err)
				}
				return
			}
		case <-c.drainWriter:
			for {
				select {
				case data := <-c.outgoing:
					if err := c.writeOutgoing(data); err != nil {
						if c.tryRequestClose(OperationWrite, err) {
							c.server.reportConnectionError(c.self, OperationWrite, err)
						}
						return
					}
				default:
					return
				}
			}
		}
	}
}

func (c *netConnection) writeOutgoing(data []byte) error {
	if err := c.writeMessage(data); err != nil {
		return err
	}
	c.server.metrics.messageSent(c.statsTransport(), uint64(len(data)-8))
	return nil
}

func (c *netConnection) quiesceReads() error {
	c.stateMu.Lock()
	if c.connectionState() == connectionOpen {
		c.state.Store(uint32(connectionDraining))
	}
	c.stateMu.Unlock()

	c.quiesceOnce.Do(func() {
		c.readCancel()
		if c.transport != nil {
			c.quiesceErr = c.transport.SetReadDeadline(time.Now())
		}
	})
	return c.quiesceErr
}

func (c *netConnection) stopSendsAndDrain(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	c.sendStopOnce.Do(func() {
		c.stateMu.Lock()
		if c.connectionState() == connectionOpen {
			c.state.Store(uint32(connectionDraining))
		}
		c.stateMu.Unlock()
		c.sendMu.Lock()
		c.acceptingSends = false
		c.sendCancel()
		c.sendMu.Unlock()

		go func() {
			c.sendWG.Wait()
			close(c.drainWriter)
		}()
	})

	select {
	case <-c.writerDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *netConnection) requestClose(op ConnectionOperation, err error) {
	c.tryRequestClose(op, err)
}

func (c *netConnection) requestCloseIfOpen(op ConnectionOperation, err error) {
	c.requestCloseMatching(op, err, true)
}

func (c *netConnection) tryRequestClose(op ConnectionOperation, err error) bool {
	return c.requestCloseMatching(op, err, false)
}

func (c *netConnection) requestCloseMatching(op ConnectionOperation, err error, onlyOpen bool) bool {
	c.stateMu.Lock()
	state := c.connectionState()
	if state == connectionClosed || state == connectionClosing || onlyOpen && state != connectionOpen {
		c.stateMu.Unlock()
		return false
	}
	c.state.Store(uint32(connectionClosing))
	c.stateMu.Unlock()

	c.closeOnce.Do(func() {
		c.closeReasonMu.Lock()
		c.closeOp = op
		c.closeErr = err
		c.closeReasonMu.Unlock()

		c.sendMu.Lock()
		c.acceptingSends = false
		c.sendCancel()
		c.sendMu.Unlock()

		c.readCancel()
		c.forceCancel()
		c.closeTransport()
	})
	return true
}

func (c *netConnection) supervise(openHookDone <-chan struct{}) {
	<-openHookDone
	c.children.Wait()
	c.finalizeOnce.Do(func() {
		c.readCancel()
		c.sendCancel()
		c.forceCancel()
		c.closeTransport()
		if c.self != nil {
			c.server.connectionManager.removeConnection(c.self)
		}
		if c.started.Load() {
			c.server.metrics.connectionClosed(c.statsTransport())
		}
		if c.self != nil {
			c.server.invokeCloseHook(c.self)
		}
		c.stateMu.Lock()
		c.state.Store(uint32(connectionClosed))
		c.stateMu.Unlock()
		close(c.done)
		if c.self != nil {
			c.server.connectionManager.markFinalized(c.self)
		}
	})
}

func (c *netConnection) wait(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *netConnection) connectionState() connectionState {
	return connectionState(c.state.Load())
}

func (c *netConnection) closeReason() (ConnectionOperation, error) {
	c.closeReasonMu.Lock()
	defer c.closeReasonMu.Unlock()
	return c.closeOp, c.closeErr
}

func (c *netConnection) frameDecoderHasPending() bool {
	c.frameDecoder.lock.Lock()
	defer c.frameDecoder.lock.Unlock()
	return len(c.frameDecoder.bytes) > 0
}

func (c *netConnection) closeTransport() {
	c.transportCloseOnce.Do(func() {
		if c.transport != nil {
			_ = c.transport.Close()
		}
	})
}
