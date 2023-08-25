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
			case <-w.ctx.Done():
				debug("Worker %d stopped", w.id)
				return
			case ctx := <-w.tasks:
				ctx.Next()
			}
		}
	}()

	debug("Worker %d started", w.id)
}

func (w *worker) stop() {
	close(w.tasks)
}
