package feishu

import (
	"context"
	"sync"
)

// taskGroup runs query work on goroutines that outlive the event handler.
//
// Feishu re-delivers an event whose handler has not returned promptly, so a
// handler that synchronously renders and sends can be redelivered mid-flight and
// answer twice. The handler therefore returns as soon as it has decided to act,
// and the reply runs here.
//
// The group owns one context created at construction (NOT derived from an event
// context, which is cancelled the moment the handler returns) and cancels it on
// Close, so shutdown stops in-flight work and waits for it to unwind.
type taskGroup struct {
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

func newTaskGroup() *taskGroup {
	ctx, cancel := context.WithCancel(context.Background())
	return &taskGroup{ctx: ctx, cancel: cancel}
}

// Go runs fn on a new goroutine with the group's context. It returns false when
// the group is already closed, in which case fn is not run and the caller must
// undo any reservation it made.
func (g *taskGroup) Go(fn func(ctx context.Context)) bool {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return false
	}
	g.wg.Add(1)
	ctx := g.ctx
	g.mu.Unlock()
	go func() {
		defer g.wg.Done()
		fn(ctx)
	}()
	return true
}

// Close cancels the group context and waits for all in-flight tasks to unwind. It
// is idempotent.
func (g *taskGroup) Close() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.closed = true
	g.cancel()
	g.mu.Unlock()
	g.wg.Wait()
}
