package ctxrun

import (
	"context"
	"sync"
)

type Runner struct {
	lock    sync.Mutex
	counter uint64
	cancel  context.CancelFunc
}

func New() *Runner {
	return &Runner{}
}

func (r *Runner) Go(parent context.Context, fn func(ctx context.Context)) {
	r.lock.Lock()
	if r.cancel != nil {
		r.cancel()
	}
	var ctx context.Context
	ctx, r.cancel = context.WithCancel(parent)
	r.counter++
	r.lock.Unlock()

	go fn(ctx)
}
