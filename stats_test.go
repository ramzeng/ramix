package ramix

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"
)

func TestServerStatsStartsAtZeroAndIsDetached(t *testing.T) {
	server, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	if got := server.Stats(); got != (ServerStats{}) {
		t.Fatalf("Stats() before startup = %+v, want zero value", got)
	}
	detached := server.Stats()
	detached.TCP.ReceivedMessages = 99
	if got := server.Stats(); got != (ServerStats{}) {
		t.Fatalf("Stats() after caller mutation = %+v, want detached zero value", got)
	}
	if err := server.Shutdown(nil); err != nil {
		t.Fatalf("Shutdown(nil) error = %v", err)
	}
	if got := server.Stats(); got != (ServerStats{}) {
		t.Fatalf("Stats() after Shutdown(nil) = %+v, want zero value", got)
	}

	server.metrics.connectionOpened(TransportTCP)
	first := server.Stats()
	server.metrics.connectionOpened(TransportTCP)
	second := server.Stats()

	if got, want := first.TCP.ActiveConnections, uint64(1); got != want {
		t.Fatalf("first snapshot TCP.ActiveConnections = %d, want %d", got, want)
	}
	if got, want := second.TCP.ActiveConnections, uint64(2); got != want {
		t.Fatalf("second snapshot TCP.ActiveConnections = %d, want %d", got, want)
	}
}

func TestServerStatsRemainReadableAcrossRunAndShutdown(t *testing.T) {
	server, err := NewServer(
		WithTransports(TransportTCP),
		WithIPVersion("tcp4"),
		WithIP("127.0.0.1"),
		WithPort(0),
		WithShutdownTimeout(time.Second),
		WithHeartbeatInterval(time.Hour),
		WithHeartbeatTimeout(2*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	runContext, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	runReturned := false
	go func() { runDone <- server.Run(runContext) }()
	t.Cleanup(func() {
		cancelRun()
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelShutdown()
		if err := server.Shutdown(shutdownContext); err != nil {
			t.Errorf("cleanup Shutdown() error = %v", err)
		}
		if runReturned {
			return
		}
		select {
		case err := <-runDone:
			if err != nil {
				t.Errorf("cleanup Run() error = %v", err)
			}
		case <-shutdownContext.Done():
			t.Errorf("cleanup timed out waiting for Run(): %v", shutdownContext.Err())
		}
	})

	deadline := time.NewTimer(3 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for server.currentState() != stateRunning || server.Address(TransportTCP) == nil {
		select {
		case err := <-runDone:
			runReturned = true
			t.Fatalf("Run() returned before readiness: %v", err)
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("server did not reach running state with a TCP address")
		}
	}

	if got := server.Stats(); got != (ServerStats{}) {
		t.Fatalf("Stats() while running = %+v, want zero value", got)
	}
	server.metrics.messageReceived(TransportTCP, 37)
	runningStats := server.Stats()
	if got, want := runningStats.TCP.ReceivedMessages, uint64(1); got != want {
		t.Fatalf("running TCP.ReceivedMessages = %d, want %d", got, want)
	}
	if got, want := runningStats.TCP.ReceivedBytes, uint64(37); got != want {
		t.Fatalf("running TCP.ReceivedBytes = %d, want %d", got, want)
	}
	if runningStats.Total != runningStats.TCP {
		t.Fatalf("running Total = %+v, want TCP %+v", runningStats.Total, runningStats.TCP)
	}
	if runningStats.WebSocket != (TransportStats{}) {
		t.Fatalf("running WebSocket = %+v, want zero value", runningStats.WebSocket)
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	select {
	case err := <-runDone:
		runReturned = true
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-shutdownContext.Done():
		t.Fatalf("timed out waiting for Run(): %v", shutdownContext.Err())
	}

	if got := server.Stats(); got != runningStats {
		t.Fatalf("Stats() after shutdown = %+v, want preserved snapshot %+v", got, runningStats)
	}
}

func TestServerMetricsSnapshotAggregatesTransports(t *testing.T) {
	var metrics serverMetrics

	metrics.connectionOpened(TransportTCP)
	metrics.connectionOpened(TransportTCP)
	metrics.connectionClosed(TransportTCP)
	metrics.taskQueued(TransportTCP)
	metrics.taskQueued(TransportTCP)
	metrics.taskDequeued(TransportTCP)
	metrics.messageReceived(TransportTCP, 11)
	metrics.messageSent(TransportTCP, 13)
	metrics.taskRejected(TransportTCP)
	metrics.connectionError(TransportTCP)
	metrics.requestCompleted(TransportTCP, 17*time.Millisecond)

	metrics.connectionOpened(TransportWebSocket)
	metrics.taskQueued(TransportWebSocket)
	metrics.messageReceived(TransportWebSocket, 19)
	metrics.messageSent(TransportWebSocket, 23)
	metrics.taskRejected(TransportWebSocket)
	metrics.connectionError(TransportWebSocket)
	metrics.requestCompleted(TransportWebSocket, 29*time.Millisecond)

	got := metrics.snapshot()
	wantTCP := TransportStats{
		ActiveConnections:      1,
		QueuedTasks:            1,
		ReceivedMessages:       1,
		ReceivedBytes:          11,
		SentMessages:           1,
		SentBytes:              13,
		RejectedTasks:          1,
		ConnectionErrors:       1,
		CompletedRequests:      1,
		TotalRequestDuration:   17 * time.Millisecond,
		MaximumRequestDuration: 17 * time.Millisecond,
	}
	wantWebSocket := TransportStats{
		ActiveConnections:      1,
		QueuedTasks:            1,
		ReceivedMessages:       1,
		ReceivedBytes:          19,
		SentMessages:           1,
		SentBytes:              23,
		RejectedTasks:          1,
		ConnectionErrors:       1,
		CompletedRequests:      1,
		TotalRequestDuration:   29 * time.Millisecond,
		MaximumRequestDuration: 29 * time.Millisecond,
	}
	wantTotal := TransportStats{
		ActiveConnections:      2,
		QueuedTasks:            2,
		ReceivedMessages:       2,
		ReceivedBytes:          30,
		SentMessages:           2,
		SentBytes:              36,
		RejectedTasks:          2,
		ConnectionErrors:       2,
		CompletedRequests:      2,
		TotalRequestDuration:   46 * time.Millisecond,
		MaximumRequestDuration: 29 * time.Millisecond,
	}

	if got.TCP != wantTCP {
		t.Errorf("snapshot TCP = %+v, want %+v", got.TCP, wantTCP)
	}
	if got.WebSocket != wantWebSocket {
		t.Errorf("snapshot WebSocket = %+v, want %+v", got.WebSocket, wantWebSocket)
	}
	if got.Total != wantTotal {
		t.Errorf("snapshot Total = %+v, want %+v", got.Total, wantTotal)
	}
}

func TestServerMetricsSaturatesAndGuardsGaugeUnderflow(t *testing.T) {
	var metrics serverMetrics

	metrics.connectionClosed(TransportTCP)
	metrics.taskDequeued(TransportTCP)
	metrics.tcp.activeConnections.Store(math.MaxUint64)
	metrics.tcp.queuedTasks.Store(math.MaxUint64)
	metrics.tcp.receivedMessages.Store(math.MaxUint64)
	metrics.tcp.receivedBytes.Store(math.MaxUint64)
	metrics.tcp.sentMessages.Store(math.MaxUint64)
	metrics.tcp.sentBytes.Store(math.MaxUint64)
	metrics.tcp.rejectedTasks.Store(math.MaxUint64)
	metrics.tcp.connectionErrors.Store(math.MaxUint64)
	metrics.tcp.completedRequests.Store(math.MaxUint64)
	metrics.tcp.totalRequestDuration.Store(math.MaxInt64)

	metrics.connectionOpened(TransportTCP)
	metrics.taskQueued(TransportTCP)
	metrics.messageReceived(TransportTCP, 1)
	metrics.messageSent(TransportTCP, 1)
	metrics.taskRejected(TransportTCP)
	metrics.connectionError(TransportTCP)
	metrics.requestCompleted(TransportTCP, time.Nanosecond)

	got := metrics.snapshot().TCP
	if got.ActiveConnections != math.MaxUint64 {
		t.Errorf("ActiveConnections = %d, want saturation at %d", got.ActiveConnections, uint64(math.MaxUint64))
	}
	if got.QueuedTasks != math.MaxUint64 {
		t.Errorf("QueuedTasks = %d, want saturation at %d", got.QueuedTasks, uint64(math.MaxUint64))
	}
	if got.ReceivedMessages != math.MaxUint64 || got.ReceivedBytes != math.MaxUint64 {
		t.Errorf("received metrics = (%d, %d), want saturation", got.ReceivedMessages, got.ReceivedBytes)
	}
	if got.SentMessages != math.MaxUint64 || got.SentBytes != math.MaxUint64 {
		t.Errorf("sent metrics = (%d, %d), want saturation", got.SentMessages, got.SentBytes)
	}
	if got.RejectedTasks != math.MaxUint64 || got.ConnectionErrors != math.MaxUint64 {
		t.Errorf("failure metrics = (%d, %d), want saturation", got.RejectedTasks, got.ConnectionErrors)
	}
	if got.CompletedRequests != math.MaxUint64 {
		t.Errorf("CompletedRequests = %d, want saturation at %d", got.CompletedRequests, uint64(math.MaxUint64))
	}
	if got.TotalRequestDuration != time.Duration(math.MaxInt64) {
		t.Errorf("TotalRequestDuration = %s, want saturation at %s", got.TotalRequestDuration, time.Duration(math.MaxInt64))
	}

	var underflow serverMetrics
	underflow.connectionClosed(TransportTCP)
	underflow.taskDequeued(TransportTCP)
	if got := underflow.snapshot().TCP; got.ActiveConnections != 0 || got.QueuedTasks != 0 {
		t.Fatalf("gauge underflow snapshot = %+v, want zero gauges", got)
	}
}

func TestServerMetricsIgnoresUnknownTransport(t *testing.T) {
	var metrics serverMetrics
	unknown := Transport(math.MaxUint8)

	metrics.connectionOpened(unknown)
	metrics.connectionClosed(unknown)
	metrics.messageReceived(unknown, 1)
	metrics.messageSent(unknown, 1)
	metrics.taskQueued(unknown)
	metrics.taskDequeued(unknown)
	metrics.taskRejected(unknown)
	metrics.connectionError(unknown)
	metrics.requestCompleted(unknown, time.Second)

	if got := metrics.snapshot(); got != (ServerStats{}) {
		t.Fatalf("snapshot after unknown transport operations = %+v, want zero value", got)
	}
}

func TestServerMetricsMaximumDurationIsMonotonicConcurrently(t *testing.T) {
	var metrics serverMetrics
	const completions = 256

	var waitGroup sync.WaitGroup
	waitGroup.Add(completions)
	for i := 1; i <= completions; i++ {
		duration := time.Duration(i) * time.Millisecond
		go func() {
			defer waitGroup.Done()
			metrics.requestCompleted(TransportWebSocket, duration)
		}()
	}
	waitGroup.Wait()

	got := metrics.snapshot().WebSocket
	if want := time.Duration(completions) * time.Millisecond; got.MaximumRequestDuration != want {
		t.Errorf("MaximumRequestDuration = %s, want %s", got.MaximumRequestDuration, want)
	}
	if got.CompletedRequests != completions {
		t.Errorf("CompletedRequests = %d, want %d", got.CompletedRequests, completions)
	}
}

func TestServerMetricsTotalSaturatesEveryAdditiveField(t *testing.T) {
	tests := []struct {
		name string
		set  func(*serverMetrics)
		get  func(TransportStats) uint64
	}{
		{
			name: "ActiveConnections",
			set: func(metrics *serverMetrics) {
				metrics.tcp.activeConnections.Store(math.MaxUint64 - 1)
				metrics.webSocket.activeConnections.Store(2)
			},
			get: func(stats TransportStats) uint64 { return stats.ActiveConnections },
		},
		{
			name: "QueuedTasks",
			set: func(metrics *serverMetrics) {
				metrics.tcp.queuedTasks.Store(math.MaxUint64 - 1)
				metrics.webSocket.queuedTasks.Store(2)
			},
			get: func(stats TransportStats) uint64 { return stats.QueuedTasks },
		},
		{
			name: "ReceivedMessages",
			set: func(metrics *serverMetrics) {
				metrics.tcp.receivedMessages.Store(math.MaxUint64 - 1)
				metrics.webSocket.receivedMessages.Store(2)
			},
			get: func(stats TransportStats) uint64 { return stats.ReceivedMessages },
		},
		{
			name: "ReceivedBytes",
			set: func(metrics *serverMetrics) {
				metrics.tcp.receivedBytes.Store(math.MaxUint64 - 1)
				metrics.webSocket.receivedBytes.Store(2)
			},
			get: func(stats TransportStats) uint64 { return stats.ReceivedBytes },
		},
		{
			name: "SentMessages",
			set: func(metrics *serverMetrics) {
				metrics.tcp.sentMessages.Store(math.MaxUint64 - 1)
				metrics.webSocket.sentMessages.Store(2)
			},
			get: func(stats TransportStats) uint64 { return stats.SentMessages },
		},
		{
			name: "SentBytes",
			set: func(metrics *serverMetrics) {
				metrics.tcp.sentBytes.Store(math.MaxUint64 - 1)
				metrics.webSocket.sentBytes.Store(2)
			},
			get: func(stats TransportStats) uint64 { return stats.SentBytes },
		},
		{
			name: "RejectedTasks",
			set: func(metrics *serverMetrics) {
				metrics.tcp.rejectedTasks.Store(math.MaxUint64 - 1)
				metrics.webSocket.rejectedTasks.Store(2)
			},
			get: func(stats TransportStats) uint64 { return stats.RejectedTasks },
		},
		{
			name: "ConnectionErrors",
			set: func(metrics *serverMetrics) {
				metrics.tcp.connectionErrors.Store(math.MaxUint64 - 1)
				metrics.webSocket.connectionErrors.Store(2)
			},
			get: func(stats TransportStats) uint64 { return stats.ConnectionErrors },
		},
		{
			name: "CompletedRequests",
			set: func(metrics *serverMetrics) {
				metrics.tcp.completedRequests.Store(math.MaxUint64 - 1)
				metrics.webSocket.completedRequests.Store(2)
			},
			get: func(stats TransportStats) uint64 { return stats.CompletedRequests },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var metrics serverMetrics
			test.set(&metrics)
			if got := test.get(metrics.snapshot().Total); got != math.MaxUint64 {
				t.Fatalf("Total.%s = %d, want saturation at %d", test.name, got, uint64(math.MaxUint64))
			}
		})
	}

	var durationMetrics serverMetrics
	durationMetrics.tcp.totalRequestDuration.Store(math.MaxInt64 - 1)
	durationMetrics.webSocket.totalRequestDuration.Store(2)
	durationMetrics.tcp.maximumRequestDuration.Store(uint64(time.Second))
	durationMetrics.webSocket.maximumRequestDuration.Store(uint64(2 * time.Second))
	durationSnapshot := durationMetrics.snapshot().Total
	if got, want := durationSnapshot.TotalRequestDuration, time.Duration(math.MaxInt64); got != want {
		t.Errorf("Total.TotalRequestDuration = %s, want saturation at %s", got, want)
	}
	if got, want := durationSnapshot.MaximumRequestDuration, 2*time.Second; got != want {
		t.Errorf("Total.MaximumRequestDuration = %s, want maximum %s", got, want)
	}
}

func TestServerMetricsNegativeDurationClampsToZero(t *testing.T) {
	var metrics serverMetrics
	metrics.requestCompleted(TransportTCP, -time.Second)

	got := metrics.snapshot().TCP
	if got.CompletedRequests != 1 {
		t.Errorf("CompletedRequests = %d, want 1", got.CompletedRequests)
	}
	if got.TotalRequestDuration != 0 {
		t.Errorf("TotalRequestDuration = %s, want 0", got.TotalRequestDuration)
	}
	if got.MaximumRequestDuration != 0 {
		t.Errorf("MaximumRequestDuration = %s, want 0", got.MaximumRequestDuration)
	}
}
