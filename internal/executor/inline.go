package executor

import (
	"context"
	"sync"
)

// InlineFunc runs an inline task (http, subflow) in the claiming instance.
type InlineFunc func(ctx context.Context, t Task) Result

// InlineExecutor runs http and subflow tasks as goroutines (§4.3 step 7).
type InlineExecutor struct {
	Run InlineFunc

	mu    sync.Mutex
	works map[string]*inlineWork
}

type inlineWork struct {
	cancel context.CancelFunc
	done   chan struct{}
	res    Result
}

// Type returns "inline".
func (e *InlineExecutor) Type() string { return Inline }

// Start runs the task in a goroutine. The reference is the task run ID.
func (e *InlineExecutor) Start(ctx context.Context, t Task) (string, error) {
	ref := t.TaskRunID.String()
	wctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	w := &inlineWork{cancel: cancel, done: make(chan struct{})}
	e.mu.Lock()
	if e.works == nil {
		e.works = map[string]*inlineWork{}
	}
	e.works[ref] = w
	e.mu.Unlock()
	go func() {
		defer close(w.done)
		w.res = e.Run(wctx, t)
	}()
	return ref, nil
}

func (e *InlineExecutor) get(ref string) (*inlineWork, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	w, ok := e.works[ref]
	return w, ok
}

// Wait blocks until the goroutine ends.
func (e *InlineExecutor) Wait(ctx context.Context, ref string) Result {
	w, ok := e.get(ref)
	if !ok {
		return Result{ExitCode: -1, Err: ErrUnknownRef}
	}
	select {
	case <-w.done:
	case <-ctx.Done():
		return Result{ExitCode: -1, Err: ctx.Err()}
	}
	e.mu.Lock()
	delete(e.works, ref)
	e.mu.Unlock()
	return w.res
}

// Cancel cancels the goroutine context.
func (e *InlineExecutor) Cancel(_ context.Context, ref string) error {
	w, ok := e.get(ref)
	if !ok {
		return ErrUnknownRef
	}
	w.cancel()
	return nil
}

// Status reports whether the goroutine still runs.
func (e *InlineExecutor) Status(_ context.Context, ref string) (Status, error) {
	w, ok := e.get(ref)
	if !ok {
		return StatusGone, nil
	}
	select {
	case <-w.done:
		return StatusGone, nil
	default:
		return StatusRunning, nil
	}
}
