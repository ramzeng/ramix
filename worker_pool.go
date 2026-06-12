package ramix

import (
	"context"
	"sync"
)

type workerPoolState uint8

const (
	workerPoolStateNew workerPoolState = iota
	workerPoolStateStarted
	workerPoolStateStopping
	workerPoolStateStopped
)

type workerPool struct {
	workers   []*worker
	submitMu  sync.RWMutex
	accepting bool
	state     workerPoolState
	tasksMu   sync.Mutex
	tasks     map[*Context]struct{}
	drainOnce sync.Once
	done      chan struct{}
	doneOnce  sync.Once
}

func newWorkerPool(count, capacity uint32) *workerPool {
	if count == 0 {
		panic("worker count must be positive")
	}
	if capacity == 0 {
		panic("worker queue capacity must be positive")
	}

	pool := &workerPool{
		workers: make([]*worker, count),
		tasks:   make(map[*Context]struct{}),
		done:    make(chan struct{}),
	}

	for i := range pool.workers {
		pool.workers[i] = newWorker(i, capacity, pool)
	}

	return pool
}

func (p *workerPool) start() {
	p.submitMu.Lock()
	if p.state != workerPoolStateNew {
		p.submitMu.Unlock()
		return
	}

	p.state = workerPoolStateStarted
	p.accepting = true
	p.submitMu.Unlock()

	for _, worker := range p.workers {
		worker.start()
	}
}

func (p *workerPool) submit(task *Context) error {
	selectedWorker := p.workers[p.workerIndex(task.Connection)]

	p.submitMu.RLock()
	defer p.submitMu.RUnlock()

	if !p.accepting {
		return ErrServerStopping
	}

	p.register(task)

	select {
	case selectedWorker.tasks <- task:
		return nil
	default:
		p.unregister(task)
		return ErrWorkerQueueFull
	}
}

func (p *workerPool) stopAcceptingAndDrain(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	p.drainOnce.Do(func() {
		p.submitMu.Lock()
		p.accepting = false

		switch p.state {
		case workerPoolStateNew:
			p.state = workerPoolStateStopped
			p.submitMu.Unlock()
			p.closeDone()
			return
		case workerPoolStateStarted:
			p.state = workerPoolStateStopping
		case workerPoolStateStopping, workerPoolStateStopped:
			p.submitMu.Unlock()
			return
		}

		for _, worker := range p.workers {
			close(worker.tasks)
		}
		p.submitMu.Unlock()

		go func() {
			for _, worker := range p.workers {
				<-worker.done
			}

			p.submitMu.Lock()
			p.state = workerPoolStateStopped
			p.submitMu.Unlock()

			p.closeDone()
		}()
	})

	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		p.forceCancel()
		return ctx.Err()
	}
}

func (p *workerPool) stopAccepting() {
	p.submitMu.Lock()
	p.accepting = false
	p.submitMu.Unlock()
}

func (p *workerPool) forceCancel() {
	for _, task := range p.snapshotTasks() {
		task.cancelTask()
	}
}

func (p *workerPool) closeDone() {
	p.doneOnce.Do(func() {
		close(p.done)
	})
}

func (p *workerPool) workerIndex(connection Connection) uint64 {
	if connection == nil {
		return 0
	}

	return connection.ID() % uint64(len(p.workers))
}

func (p *workerPool) register(task *Context) {
	p.tasksMu.Lock()
	defer p.tasksMu.Unlock()

	p.tasks[task] = struct{}{}
}

func (p *workerPool) unregister(task *Context) {
	p.tasksMu.Lock()
	defer p.tasksMu.Unlock()

	delete(p.tasks, task)
}

func (p *workerPool) snapshotTasks() []*Context {
	p.tasksMu.Lock()
	defer p.tasksMu.Unlock()

	tasks := make([]*Context, 0, len(p.tasks))
	for task := range p.tasks {
		tasks = append(tasks, task)
	}

	return tasks
}
