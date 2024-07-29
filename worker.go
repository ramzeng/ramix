package ramix

import "context"

type worker struct {
	id     int
	tasks  chan *Context
	ctx    context.Context
	cancel context.CancelFunc
}

func (w *worker) start() {
	go func() {
		for {
			select {
			// if server use worker pool, this context is the server's context
			// else, this context is the connection's context
			case <-w.ctx.Done():
				debug("Worker %d stopped", w.id)
				return
			case ctx := <-w.tasks:
				// If the context is nil, it means the worker is stopped
				if ctx == nil {
					debug("Worker %d stopped", w.id)
					return
				}
				ctx.Next()
			}
		}
	}()

	debug("Worker %d started", w.id)
}

func (w *worker) stop() {
	w.cancel()
	close(w.tasks)
}

func newWorker(workerID int, maxTasksCount uint32) *worker {
	w := &worker{
		id:    workerID,
		tasks: make(chan *Context, maxTasksCount),
	}

	w.ctx, w.cancel = context.WithCancel(context.Background())

	return w
}
