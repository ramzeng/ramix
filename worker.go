package ramix

type worker struct {
	id    int
	tasks chan *Context
	pool  *workerPool
	done  chan struct{}
}

func (w *worker) start() {
	go func() {
		defer close(w.done)

		for task := range w.tasks {
			if task == nil {
				continue
			}

			func() {
				defer task.finish()
				defer w.pool.unregister(task)

				if task.Err() != nil {
					return
				}

				task.Next()
			}()
		}

		debug("Worker %d stopped", w.id)
	}()

	debug("Worker %d started", w.id)
}

func newWorker(workerID int, maxTasksCount uint32, pool *workerPool) *worker {
	return &worker{
		id:    workerID,
		tasks: make(chan *Context, maxTasksCount),
		pool:  pool,
		done:  make(chan struct{}),
	}
}
