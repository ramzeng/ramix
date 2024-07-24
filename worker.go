package ramix

import "context"

type worker struct {
	id    int
	tasks chan *Context
	ctx   context.Context
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
	close(w.tasks)
}

func newWorker(workerID int, maxTasksCount uint32, ctx context.Context) *worker {
	return &worker{
		id:    workerID,
		tasks: make(chan *Context, maxTasksCount),
		ctx:   ctx,
	}
}
