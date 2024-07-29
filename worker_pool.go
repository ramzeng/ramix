package ramix

type WorkerPool interface {
	init()
	start()
	stop()
	submitTask(ctx *Context)
}

type RoundRobinWorkerPool struct {
	workers             []*worker
	maxWorkerTasksCount uint32
}

func (p *RoundRobinWorkerPool) init() {
	for i := 0; i < len(p.workers); i++ {
		p.workers[i] = newWorker(i, p.maxWorkerTasksCount)
	}
}

func (p *RoundRobinWorkerPool) start() {
	for i := 0; i < len(p.workers); i++ {
		p.workers[i].start()
	}
}

func (p *RoundRobinWorkerPool) stop() {
	for i := 0; i < len(p.workers); i++ {
		p.workers[i].stop()
	}
}

func (p *RoundRobinWorkerPool) submitTask(ctx *Context) {
	p.workers[ctx.Connection.ID()%uint64(len(p.workers))].tasks <- ctx
}

func NewRoundRobinWorkerPool(workersCount uint32, maxWorkerTasksCount uint32) *RoundRobinWorkerPool {
	return &RoundRobinWorkerPool{
		workers:             make([]*worker, workersCount),
		maxWorkerTasksCount: maxWorkerTasksCount,
	}
}
