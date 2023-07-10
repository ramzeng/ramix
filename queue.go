package ramix

type queue struct {
	contextChannel chan *Context
	workersCount   uint32
}

func (q *queue) start() {
	for i := 0; i < int(q.workersCount); i++ {
		go func() {
			for {
				select {
				case context := <-q.contextChannel:
					context.Next()
				}
			}
		}()
	}
}
