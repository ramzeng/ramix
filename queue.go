package ramix

import "context"

type queue struct {
	contextChannel chan *Context
	workersCount   uint32
	ctx            context.Context
}

func (q *queue) close() {
	close(q.contextChannel)
}

func (q *queue) start() {
	for i := 0; i < int(q.workersCount); i++ {
		go func(id int) {
			for {
				select {
				case <-q.ctx.Done():
					debug("Worker %d stopped", id)
					return
				case ctx := <-q.contextChannel:
					ctx.Next()
				}
			}
		}(i)
	}

	debug("Queue started, Workers count: %d", q.workersCount)
}
