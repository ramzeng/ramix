package ramix

import (
	"context"
	"errors"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"
)

type testConnection struct {
	id uint64
}

func (c *testConnection) ID() uint64              { return c.id }
func (c *testConnection) RemoteAddress() net.Addr { return nil }
func (c *testConnection) Send(context.Context, uint32, []byte) error {
	return nil
}

func TestWorkerPoolSameConnectionTasksExecuteInOrder(t *testing.T) {
	pool := newWorkerPool(2, 2)
	pool.start()

	conn := &testConnection{id: 7}
	var (
		mu     sync.Mutex
		got    []int
		taskWG sync.WaitGroup
	)

	taskWG.Add(2)
	for _, want := range []int{1, 2} {
		task := newContext(context.Background(), conn, nil)
		value := want
		task.handlers = []Handler{
			func(*Context) {
				defer taskWG.Done()
				mu.Lock()
				got = append(got, value)
				mu.Unlock()
			},
		}

		if err := pool.submit(task); err != nil {
			t.Fatalf("submit(%d) error = %v", value, err)
		}
	}

	done := make(chan struct{})
	go func() {
		taskWG.Wait()
		close(done)
	}()
	waitForSignal(t, done, "same-connection tasks")

	if err := pool.stopAcceptingAndDrain(context.Background()); err != nil {
		t.Fatalf("stopAcceptingAndDrain() error = %v", err)
	}

	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("execution order = %v, want [1 2]", got)
	}
}

func TestWorkerPoolSubmitBeforeStartReturnsServerStopping(t *testing.T) {
	pool := newWorkerPool(1, 1)

	task := newContext(context.Background(), &testConnection{id: 1}, nil)
	task.handlers = []Handler{func(*Context) {}}

	if err := pool.submit(task); !errors.Is(err, ErrServerStopping) {
		t.Fatalf("submit() before start error = %v, want %v", err, ErrServerStopping)
	}
}

func TestWorkerPoolRepeatedStartIsSafeAndTasksExecuteOnce(t *testing.T) {
	pool := newWorkerPool(1, 1)
	pool.start()
	pool.start()

	var (
		mu        sync.Mutex
		executed  int
		completed = make(chan struct{})
	)

	task := newContext(context.Background(), &testConnection{id: 1}, nil)
	task.handlers = []Handler{
		func(*Context) {
			mu.Lock()
			executed++
			mu.Unlock()
			close(completed)
		},
	}

	if err := pool.submit(task); err != nil {
		t.Fatalf("submit() error = %v", err)
	}
	waitForSignal(t, completed, "repeated-start task completion")

	if err := pool.stopAcceptingAndDrain(context.Background()); err != nil {
		t.Fatalf("stopAcceptingAndDrain() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if executed != 1 {
		t.Fatalf("task execution count = %d, want 1", executed)
	}
}

func TestWorkerPoolDifferentConnectionsUseDeterministicWorkers(t *testing.T) {
	pool := newWorkerPool(2, 1)
	pool.start()

	block := make(chan struct{})
	startedFirst := make(chan struct{})
	startedSecond := make(chan struct{})

	first := newContext(context.Background(), &testConnection{id: 1}, nil)
	first.handlers = []Handler{
		func(*Context) {
			close(startedFirst)
			<-block
		},
	}

	second := newContext(context.Background(), &testConnection{id: 2}, nil)
	second.handlers = []Handler{
		func(*Context) {
			close(startedSecond)
		},
	}

	if err := pool.submit(first); err != nil {
		t.Fatalf("submit(first) error = %v", err)
	}
	waitForSignal(t, startedFirst, "first task start")

	if err := pool.submit(second); err != nil {
		t.Fatalf("submit(second) error = %v", err)
	}
	waitForSignal(t, startedSecond, "second task start")

	close(block)

	if err := pool.stopAcceptingAndDrain(context.Background()); err != nil {
		t.Fatalf("stopAcceptingAndDrain() error = %v", err)
	}
}

func TestWorkerPoolSubmitReturnsQueueFullImmediately(t *testing.T) {
	pool := newWorkerPool(1, 1)
	pool.start()

	block := make(chan struct{})
	started := make(chan struct{})

	first := newContext(context.Background(), &testConnection{id: 1}, nil)
	first.handlers = []Handler{
		func(*Context) {
			close(started)
			<-block
		},
	}

	second := newContext(context.Background(), &testConnection{id: 1}, nil)
	second.handlers = []Handler{func(*Context) {}}

	third := newContext(context.Background(), &testConnection{id: 1}, nil)
	third.handlers = []Handler{func(*Context) {}}

	if err := pool.submit(first); err != nil {
		t.Fatalf("submit(first) error = %v", err)
	}
	waitForSignal(t, started, "first task start")

	if err := pool.submit(second); err != nil {
		t.Fatalf("submit(second) error = %v", err)
	}

	if err := pool.submit(third); !errors.Is(err, ErrWorkerQueueFull) {
		t.Fatalf("submit(third) error = %v, want %v", err, ErrWorkerQueueFull)
	}

	close(block)

	if err := pool.stopAcceptingAndDrain(context.Background()); err != nil {
		t.Fatalf("stopAcceptingAndDrain() error = %v", err)
	}
}

func TestWorkerPoolMetricsTrackQueuedAndRejectedTasks(t *testing.T) {
	pool := newWorkerPool(1, 1)
	pool.start()

	var metrics serverMetrics
	block := make(chan struct{})
	started := make(chan struct{})

	first := newMetricsContext(&metrics, TransportTCP, 1)
	first.handlers = []Handler{
		func(*Context) {
			close(started)
			<-block
		},
	}

	second := newMetricsContext(&metrics, TransportTCP, 1)
	second.handlers = []Handler{func(*Context) {}}

	third := newMetricsContext(&metrics, TransportTCP, 1)
	third.handlers = []Handler{func(*Context) {}}

	if err := pool.submit(first); err != nil {
		t.Fatalf("submit(first) error = %v", err)
	}
	waitForSignal(t, started, "first metric task start")

	if err := pool.submit(second); err != nil {
		t.Fatalf("submit(second) error = %v", err)
	}
	if got := metrics.snapshot().TCP.QueuedTasks; got != 1 {
		t.Fatalf("queued tasks while second task waits = %d, want 1", got)
	}

	if err := pool.submit(third); !errors.Is(err, ErrWorkerQueueFull) {
		t.Fatalf("submit(third) error = %v, want %v", err, ErrWorkerQueueFull)
	}
	if got := metrics.snapshot().TCP.RejectedTasks; got != 1 {
		t.Fatalf("rejected tasks after queue-full submit = %d, want 1", got)
	}

	close(block)

	if err := pool.stopAcceptingAndDrain(context.Background()); err != nil {
		t.Fatalf("stopAcceptingAndDrain() error = %v", err)
	}
	if got := metrics.snapshot().TCP.QueuedTasks; got != 0 {
		t.Fatalf("queued tasks after drain = %d, want 0", got)
	}
}

func TestWorkerMetricsCanceledTaskIsDequeuedButNotCompleted(t *testing.T) {
	pool := newWorkerPool(1, 1)
	pool.start()

	var metrics serverMetrics
	task := newMetricsContext(&metrics, TransportTCP, 1)
	task.cancelTask()
	task.handlers = []Handler{
		func(*Context) {
			t.Fatal("canceled task handler should not run")
		},
	}

	if err := pool.submit(task); err != nil {
		t.Fatalf("submit(canceled) error = %v", err)
	}
	if err := pool.stopAcceptingAndDrain(context.Background()); err != nil {
		t.Fatalf("stopAcceptingAndDrain() error = %v", err)
	}

	stats := metrics.snapshot().TCP
	if stats.QueuedTasks != 0 {
		t.Fatalf("queued tasks after canceled task drains = %d, want 0", stats.QueuedTasks)
	}
	if stats.CompletedRequests != 0 {
		t.Fatalf("completed requests after canceled task drains = %d, want 0", stats.CompletedRequests)
	}
}

func TestWorkerPoolMetricsDoNotRejectWhenStopping(t *testing.T) {
	pool := newWorkerPool(1, 1)
	pool.start()
	pool.stopAccepting()
	t.Cleanup(func() {
		if err := pool.stopAcceptingAndDrain(context.Background()); err != nil {
			t.Fatalf("stopAcceptingAndDrain() error = %v", err)
		}
	})

	var metrics serverMetrics
	task := newMetricsContext(&metrics, TransportTCP, 1)
	task.handlers = []Handler{func(*Context) {}}

	if err := pool.submit(task); !errors.Is(err, ErrServerStopping) {
		t.Fatalf("submit() while stopping error = %v, want %v", err, ErrServerStopping)
	}
	stats := metrics.snapshot().TCP
	if stats.QueuedTasks != 0 {
		t.Fatalf("queued tasks after stopped submit = %d, want 0", stats.QueuedTasks)
	}
	if stats.RejectedTasks != 0 {
		t.Fatalf("rejected tasks after stopped submit = %d, want 0", stats.RejectedTasks)
	}
}

func TestWorkerMetricsRecordCompletedRequestDuration(t *testing.T) {
	pool := newWorkerPool(1, 1)
	pool.start()

	var metrics serverMetrics
	task := newMetricsContext(&metrics, TransportWebSocket, 1)
	task.handlers = []Handler{
		func(*Context) {
			time.Sleep(5 * time.Millisecond)
		},
	}

	if err := pool.submit(task); err != nil {
		t.Fatalf("submit(task) error = %v", err)
	}
	if err := pool.stopAcceptingAndDrain(context.Background()); err != nil {
		t.Fatalf("stopAcceptingAndDrain() error = %v", err)
	}

	stats := metrics.snapshot().WebSocket
	if stats.CompletedRequests != 1 {
		t.Fatalf("completed requests = %d, want 1", stats.CompletedRequests)
	}
	if stats.TotalRequestDuration < 5*time.Millisecond {
		t.Fatalf("total request duration = %s, want at least 5ms", stats.TotalRequestDuration)
	}
	if stats.MaximumRequestDuration != stats.TotalRequestDuration {
		t.Fatalf("maximum request duration = %s, want total duration %s", stats.MaximumRequestDuration, stats.TotalRequestDuration)
	}
}

func TestWorkerMetricsExcludeQueueWaitFromDuration(t *testing.T) {
	pool := newWorkerPool(1, 1)
	pool.start()

	block := make(chan struct{})
	started := make(chan struct{})

	first := newContext(context.Background(), &testConnection{id: 1}, nil)
	first.handlers = []Handler{
		func(*Context) {
			close(started)
			<-block
		},
	}

	var metrics serverMetrics
	second := newMetricsContext(&metrics, TransportTCP, 1)
	second.handlers = []Handler{
		func(*Context) {
			time.Sleep(5 * time.Millisecond)
		},
	}

	if err := pool.submit(first); err != nil {
		t.Fatalf("submit(first) error = %v", err)
	}
	waitForSignal(t, started, "unattributed task start")

	if err := pool.submit(second); err != nil {
		t.Fatalf("submit(second) error = %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	close(block)

	if err := pool.stopAcceptingAndDrain(context.Background()); err != nil {
		t.Fatalf("stopAcceptingAndDrain() error = %v", err)
	}

	duration := metrics.snapshot().TCP.TotalRequestDuration
	if duration < 5*time.Millisecond {
		t.Fatalf("total request duration = %s, want at least 5ms", duration)
	}
	if duration >= 100*time.Millisecond {
		t.Fatalf("total request duration = %s, want queue wait excluded below 100ms", duration)
	}
}

func TestWorkerPoolStopAcceptingAndDrainDrainsQueuedTasks(t *testing.T) {
	pool := newWorkerPool(1, 2)
	pool.start()

	block := make(chan struct{})
	started := make(chan struct{})
	drainedQueued := make(chan struct{})

	first := newContext(context.Background(), &testConnection{id: 1}, nil)
	first.handlers = []Handler{
		func(*Context) {
			close(started)
			<-block
		},
	}

	second := newContext(context.Background(), &testConnection{id: 1}, nil)
	second.handlers = []Handler{
		func(*Context) {
			close(drainedQueued)
		},
	}

	if err := pool.submit(first); err != nil {
		t.Fatalf("submit(first) error = %v", err)
	}
	waitForSignal(t, started, "first task start")

	if err := pool.submit(second); err != nil {
		t.Fatalf("submit(second) error = %v", err)
	}

	drainDone := make(chan error, 1)
	go func() {
		drainDone <- pool.stopAcceptingAndDrain(context.Background())
	}()

	close(block)
	waitForSignal(t, drainedQueued, "queued task drain")

	if err := <-drainDone; err != nil {
		t.Fatalf("stopAcceptingAndDrain() error = %v", err)
	}

	task := newContext(context.Background(), &testConnection{id: 1}, nil)
	task.handlers = []Handler{func(*Context) {}}

	if err := pool.submit(task); !errors.Is(err, ErrServerStopping) {
		t.Fatalf("submit() after drain error = %v, want %v", err, ErrServerStopping)
	}
}

func TestWorkerPoolDrainBeforeStartStopsPoolPermanently(t *testing.T) {
	pool := newWorkerPool(1, 1)

	drainDone := make(chan error, 1)
	go func() {
		drainDone <- pool.stopAcceptingAndDrain(context.Background())
	}()

	select {
	case err := <-drainDone:
		if err != nil {
			t.Fatalf("stopAcceptingAndDrain() before start error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stopAcceptingAndDrain() before start did not return promptly")
	}

	waitForSignal(t, pool.done, "pre-start drain completion")

	pool.start()
	pool.start()

	task := newContext(context.Background(), &testConnection{id: 1}, nil)
	task.handlers = []Handler{func(*Context) {}}

	if err := pool.submit(task); !errors.Is(err, ErrServerStopping) {
		t.Fatalf("submit() after pre-start drain error = %v, want %v", err, ErrServerStopping)
	}
}

func TestWorkerPoolForceCancelCancelsRunningTaskAndSkipsQueuedCanceledTask(t *testing.T) {
	pool := newWorkerPool(1, 1)
	pool.start()

	runningStarted := make(chan struct{})
	runningCanceled := make(chan struct{})
	queuedMiddlewareRan := make(chan struct{})

	first := newContext(context.Background(), &testConnection{id: 1}, nil)
	first.handlers = []Handler{
		func(ctx *Context) {
			close(runningStarted)
			<-ctx.Done()
			close(runningCanceled)
		},
	}

	second := newContext(context.Background(), &testConnection{id: 1}, nil)
	second.handlers = []Handler{
		func(*Context) {
			close(queuedMiddlewareRan)
		},
		func(*Context) {
			t.Fatal("queued canceled task handler should not run")
		},
	}

	if err := pool.submit(first); err != nil {
		t.Fatalf("submit(first) error = %v", err)
	}
	waitForSignal(t, runningStarted, "running task start")

	if err := pool.submit(second); err != nil {
		t.Fatalf("submit(second) error = %v", err)
	}

	pool.forceCancel()
	waitForSignal(t, runningCanceled, "running task cancellation")

	if err := pool.stopAcceptingAndDrain(context.Background()); err != nil {
		t.Fatalf("stopAcceptingAndDrain() error = %v", err)
	}

	assertNotClosed(t, queuedMiddlewareRan, "queued canceled task middleware execution")
}

func TestWorkerPoolStopAcceptingAndDrainReturnsDeadlineAndCancelsTask(t *testing.T) {
	pool := newWorkerPool(1, 1)
	pool.start()

	started := make(chan struct{})
	canceled := make(chan struct{})

	first := newContext(context.Background(), &testConnection{id: 1}, nil)
	first.handlers = []Handler{
		func(ctx *Context) {
			close(started)
			<-ctx.Done()
			close(canceled)
		},
	}

	if err := pool.submit(first); err != nil {
		t.Fatalf("submit(first) error = %v", err)
	}
	waitForSignal(t, started, "running task start")

	drainCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := pool.stopAcceptingAndDrain(drainCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stopAcceptingAndDrain() error = %v, want %v", err, context.DeadlineExceeded)
	}

	waitForSignal(t, canceled, "task cancellation after deadline")
}

func TestWorkerPoolRepeatedDrainAndForceCancelAreSafe(t *testing.T) {
	pool := newWorkerPool(1, 1)
	pool.start()

	started := make(chan struct{})

	first := newContext(context.Background(), &testConnection{id: 1}, nil)
	first.handlers = []Handler{
		func(ctx *Context) {
			close(started)
			<-ctx.Done()
		},
	}

	if err := pool.submit(first); err != nil {
		t.Fatalf("submit(first) error = %v", err)
	}
	waitForSignal(t, started, "running task start")

	var callers sync.WaitGroup
	errs := make(chan error, 4)

	for i := 0; i < 4; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			errs <- pool.stopAcceptingAndDrain(context.Background())
		}()
	}

	for i := 0; i < 4; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			pool.forceCancel()
		}()
	}

	callers.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("stopAcceptingAndDrain() concurrent error = %v", err)
		}
	}
}

func TestWorkerPoolStopAcceptingAndDrainCanceledContextStopsIntakeBeforeReturn(t *testing.T) {
	originalMaxProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(originalMaxProcs)

	for i := 0; i < 200; i++ {
		pool := newWorkerPool(1, 1)
		pool.start()

		started := make(chan struct{})
		canceled := make(chan struct{})

		running := newContext(context.Background(), &testConnection{id: 1}, nil)
		running.handlers = []Handler{
			func(ctx *Context) {
				close(started)
				<-ctx.Done()
				close(canceled)
			},
		}

		if err := pool.submit(running); err != nil {
			t.Fatalf("iteration %d: submit(running) error = %v", i, err)
		}
		waitForSignal(t, started, "running task start")

		drainCtx, cancel := context.WithCancel(context.Background())
		cancel()

		err := pool.stopAcceptingAndDrain(drainCtx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d: stopAcceptingAndDrain() error = %v, want %v", i, err, context.Canceled)
		}

		waitForSignal(t, canceled, "running task cancellation")

		queued := newContext(context.Background(), &testConnection{id: 1}, nil)
		queued.handlers = []Handler{func(*Context) {}}

		if err := pool.submit(queued); !errors.Is(err, ErrServerStopping) {
			t.Fatalf("iteration %d: submit() after canceled drain error = %v, want %v", i, err, ErrServerStopping)
		}

		waitForSignal(t, pool.done, "worker pool completion")
	}
}

func TestWorkerPoolStopAcceptingAndDrainTimeoutDoesNotBlockOnNonCooperativeHandler(t *testing.T) {
	pool := newWorkerPool(1, 1)
	pool.start()

	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once

	t.Cleanup(func() {
		releaseOnce.Do(func() {
			close(release)
		})
	})

	running := newContext(context.Background(), &testConnection{id: 1}, nil)
	running.handlers = []Handler{
		func(*Context) {
			close(started)
			<-release
		},
	}

	if err := pool.submit(running); err != nil {
		t.Fatalf("submit(running) error = %v", err)
	}
	waitForSignal(t, started, "non-cooperative task start")

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := pool.stopAcceptingAndDrain(canceledCtx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stopAcceptingAndDrain(canceled) error = %v, want %v", err, context.Canceled)
	}

	queued := newContext(context.Background(), &testConnection{id: 1}, nil)
	queued.handlers = []Handler{func(*Context) {}}

	if err := pool.submit(queued); !errors.Is(err, ErrServerStopping) {
		t.Fatalf("submit() after canceled drain error = %v, want %v", err, ErrServerStopping)
	}

	expiredCtx, expireCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expireCancel()

	err = pool.stopAcceptingAndDrain(expiredCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stopAcceptingAndDrain(expired) error = %v, want %v", err, context.DeadlineExceeded)
	}

	releaseOnce.Do(func() {
		close(release)
	})

	if err := pool.stopAcceptingAndDrain(context.Background()); err != nil {
		t.Fatalf("stopAcceptingAndDrain(background) error = %v, want nil", err)
	}
}

func TestWorkerPoolConcurrentStartAndDrainNeverReopensPool(t *testing.T) {
	originalMaxProcs := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(originalMaxProcs)

	for i := 0; i < 200; i++ {
		pool := newWorkerPool(1, 1)

		var calls sync.WaitGroup
		startDone := make(chan struct{})
		drainDone := make(chan error, 1)

		calls.Add(2)
		go func() {
			defer calls.Done()
			pool.start()
			close(startDone)
		}()
		go func() {
			defer calls.Done()
			drainDone <- pool.stopAcceptingAndDrain(context.Background())
		}()

		calls.Wait()
		waitForSignal(t, startDone, "concurrent start completion")

		if err := <-drainDone; err != nil {
			t.Fatalf("iteration %d: stopAcceptingAndDrain() error = %v", i, err)
		}

		task := newContext(context.Background(), &testConnection{id: 1}, nil)
		task.handlers = []Handler{func(*Context) {}}

		if err := pool.submit(task); !errors.Is(err, ErrServerStopping) {
			t.Fatalf("iteration %d: submit() after concurrent start/drain error = %v, want %v", i, err, ErrServerStopping)
		}
	}
}

func waitForSignal(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func assertNotClosed(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()

	select {
	case <-ch:
		t.Fatalf("%s unexpectedly closed", label)
	default:
	}
}

func newMetricsContext(metrics *serverMetrics, transport Transport, connectionID uint64) *Context {
	task := newContext(context.Background(), &testConnection{id: connectionID}, nil)
	task.metrics = metrics
	task.metricTransport = transport
	return task
}
