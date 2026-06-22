package ramix

import (
	"math"
	"sync/atomic"
	"time"
)

// ServerStats is a detached, approximate point-in-time snapshot of a server's
// lifetime-cumulative counters and current gauges. Its fields are loaded
// independently and are not a transactional view of concurrent activity.
type ServerStats struct {
	// Total aggregates the TCP and WebSocket statistics with saturated sums.
	Total TransportStats
	// TCP contains statistics accumulated over the server's lifetime for TCP.
	TCP TransportStats
	// WebSocket contains statistics accumulated over the server's lifetime for
	// WebSocket connections.
	WebSocket TransportStats
}

// TransportStats is an approximate point-in-time snapshot of one transport's
// lifetime-cumulative counters and current gauges. Concurrent activity may
// occur between individual field loads.
type TransportStats struct {
	// ActiveConnections is the approximate number of currently open connections.
	ActiveConnections uint64
	// QueuedTasks is the approximate number of tasks currently waiting to run.
	QueuedTasks uint64
	// ReceivedMessages is the lifetime-cumulative number of received messages.
	ReceivedMessages uint64
	// ReceivedBytes is the lifetime-cumulative number of received message bytes.
	ReceivedBytes uint64
	// SentMessages is the lifetime-cumulative number of sent messages.
	SentMessages uint64
	// SentBytes is the lifetime-cumulative number of sent message bytes.
	SentBytes uint64
	// RejectedTasks is the lifetime-cumulative number of rejected tasks.
	RejectedTasks uint64
	// ConnectionErrors is the lifetime-cumulative number of connection errors.
	ConnectionErrors uint64
	// CompletedRequests is the lifetime-cumulative number of completed requests.
	CompletedRequests uint64
	// TotalRequestDuration is the saturated lifetime-cumulative request duration.
	TotalRequestDuration time.Duration
	// MaximumRequestDuration is the greatest request duration observed over the
	// server's lifetime.
	MaximumRequestDuration time.Duration
}

type serverMetrics struct {
	tcp       transportMetrics
	webSocket transportMetrics
}

type transportMetrics struct {
	activeConnections      atomic.Uint64
	queuedTasks            atomic.Uint64
	receivedMessages       atomic.Uint64
	receivedBytes          atomic.Uint64
	sentMessages           atomic.Uint64
	sentBytes              atomic.Uint64
	rejectedTasks          atomic.Uint64
	connectionErrors       atomic.Uint64
	completedRequests      atomic.Uint64
	totalRequestDuration   atomic.Uint64
	maximumRequestDuration atomic.Uint64
}

// Stats returns a detached, approximate point-in-time snapshot of the
// server's lifetime-cumulative statistics.
func (s *Server) Stats() ServerStats {
	return s.metrics.snapshot()
}

type statsTransportProvider interface {
	statsTransport() Transport
}

func transportForStats(connection Connection) Transport {
	if connection == nil {
		return 0
	}
	provider, ok := connection.(statsTransportProvider)
	if !ok {
		return 0
	}
	return provider.statsTransport()
}

func (m *serverMetrics) forTransport(transport Transport) *transportMetrics {
	switch transport {
	case TransportTCP:
		return &m.tcp
	case TransportWebSocket:
		return &m.webSocket
	default:
		return nil
	}
}

func (m *serverMetrics) connectionOpened(transport Transport) {
	metrics := m.forTransport(transport)
	if metrics == nil {
		return
	}
	saturatingAdd(&metrics.activeConnections, 1, math.MaxUint64)
}

func (m *serverMetrics) connectionClosed(transport Transport) {
	metrics := m.forTransport(transport)
	if metrics == nil {
		return
	}
	decrementGauge(&metrics.activeConnections)
}

func (m *serverMetrics) messageReceived(transport Transport, bytes uint64) {
	metrics := m.forTransport(transport)
	if metrics == nil {
		return
	}
	saturatingAdd(&metrics.receivedMessages, 1, math.MaxUint64)
	saturatingAdd(&metrics.receivedBytes, bytes, math.MaxUint64)
}

func (m *serverMetrics) messageSent(transport Transport, bytes uint64) {
	metrics := m.forTransport(transport)
	if metrics == nil {
		return
	}
	saturatingAdd(&metrics.sentMessages, 1, math.MaxUint64)
	saturatingAdd(&metrics.sentBytes, bytes, math.MaxUint64)
}

func (m *serverMetrics) taskQueued(transport Transport) {
	metrics := m.forTransport(transport)
	if metrics == nil {
		return
	}
	saturatingAdd(&metrics.queuedTasks, 1, math.MaxUint64)
}

func (m *serverMetrics) taskDequeued(transport Transport) {
	metrics := m.forTransport(transport)
	if metrics == nil {
		return
	}
	decrementGauge(&metrics.queuedTasks)
}

func (m *serverMetrics) taskRejected(transport Transport) {
	metrics := m.forTransport(transport)
	if metrics == nil {
		return
	}
	saturatingAdd(&metrics.rejectedTasks, 1, math.MaxUint64)
}

func (m *serverMetrics) connectionError(transport Transport) {
	metrics := m.forTransport(transport)
	if metrics == nil {
		return
	}
	saturatingAdd(&metrics.connectionErrors, 1, math.MaxUint64)
}

func (m *serverMetrics) requestCompleted(transport Transport, duration time.Duration) {
	metrics := m.forTransport(transport)
	if metrics == nil {
		return
	}
	saturatingAdd(&metrics.completedRequests, 1, math.MaxUint64)
	if duration <= 0 {
		return
	}

	durationValue := uint64(duration)
	saturatingAdd(&metrics.totalRequestDuration, durationValue, math.MaxInt64)
	updateMaximum(&metrics.maximumRequestDuration, durationValue)
}

func (m *serverMetrics) snapshot() ServerStats {
	tcp := m.tcp.snapshot()
	webSocket := m.webSocket.snapshot()
	return ServerStats{
		Total:     combineTransportStats(tcp, webSocket),
		TCP:       tcp,
		WebSocket: webSocket,
	}
}

func (m *transportMetrics) snapshot() TransportStats {
	return TransportStats{
		ActiveConnections:      m.activeConnections.Load(),
		QueuedTasks:            m.queuedTasks.Load(),
		ReceivedMessages:       m.receivedMessages.Load(),
		ReceivedBytes:          m.receivedBytes.Load(),
		SentMessages:           m.sentMessages.Load(),
		SentBytes:              m.sentBytes.Load(),
		RejectedTasks:          m.rejectedTasks.Load(),
		ConnectionErrors:       m.connectionErrors.Load(),
		CompletedRequests:      m.completedRequests.Load(),
		TotalRequestDuration:   counterDuration(m.totalRequestDuration.Load()),
		MaximumRequestDuration: counterDuration(m.maximumRequestDuration.Load()),
	}
}

func combineTransportStats(first, second TransportStats) TransportStats {
	return TransportStats{
		ActiveConnections:      saturatedSum(first.ActiveConnections, second.ActiveConnections, math.MaxUint64),
		QueuedTasks:            saturatedSum(first.QueuedTasks, second.QueuedTasks, math.MaxUint64),
		ReceivedMessages:       saturatedSum(first.ReceivedMessages, second.ReceivedMessages, math.MaxUint64),
		ReceivedBytes:          saturatedSum(first.ReceivedBytes, second.ReceivedBytes, math.MaxUint64),
		SentMessages:           saturatedSum(first.SentMessages, second.SentMessages, math.MaxUint64),
		SentBytes:              saturatedSum(first.SentBytes, second.SentBytes, math.MaxUint64),
		RejectedTasks:          saturatedSum(first.RejectedTasks, second.RejectedTasks, math.MaxUint64),
		ConnectionErrors:       saturatedSum(first.ConnectionErrors, second.ConnectionErrors, math.MaxUint64),
		CompletedRequests:      saturatedSum(first.CompletedRequests, second.CompletedRequests, math.MaxUint64),
		TotalRequestDuration:   time.Duration(saturatedSum(uint64(first.TotalRequestDuration), uint64(second.TotalRequestDuration), math.MaxInt64)),
		MaximumRequestDuration: maxDuration(first.MaximumRequestDuration, second.MaximumRequestDuration),
	}
}

func saturatingAdd(counter *atomic.Uint64, delta, limit uint64) {
	if delta == 0 {
		return
	}
	for {
		current := counter.Load()
		if current >= limit {
			return
		}
		next := saturatedSum(current, delta, limit)
		if counter.CompareAndSwap(current, next) {
			return
		}
	}
}

func decrementGauge(counter *atomic.Uint64) {
	for {
		current := counter.Load()
		if current == 0 {
			return
		}
		if counter.CompareAndSwap(current, current-1) {
			return
		}
	}
}

func updateMaximum(counter *atomic.Uint64, candidate uint64) {
	for {
		current := counter.Load()
		if candidate <= current {
			return
		}
		if counter.CompareAndSwap(current, candidate) {
			return
		}
	}
}

func saturatedSum(first, second, limit uint64) uint64 {
	if first >= limit || second > limit-first {
		return limit
	}
	return first + second
}

func counterDuration(value uint64) time.Duration {
	if value > math.MaxInt64 {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(value)
}

func maxDuration(first, second time.Duration) time.Duration {
	if first >= second {
		return first
	}
	return second
}
