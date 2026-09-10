// Package executor starts task work on inline, process, docker and kubernetes
// executors behind one interface (REQ-EXR-001).
package executor

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/flow"
)

// Executor types.
const (
	Inline     = "inline"
	Process    = "process"
	Docker     = "docker"
	Kubernetes = "kubernetes"
)

// Task is the work that an executor starts.
type Task struct {
	TaskRunID   uuid.UUID
	ExecutionID uuid.UUID
	TaskKey     string
	Attempt     int
	Pool        string
	// Env holds SLUICE_API_URL, SLUICE_RUN_TOKEN and SLUICE_TASK_RUN_ID for the runner.
	Env      map[string]string
	Executor flow.Executor
	Timeout  time.Duration
}

// Result is the end of the work as seen by the executor. The task state comes from
// the runner complete call; the executor result only tells that the work ended.
type Result struct {
	ExitCode int
	Err      error
	// Reason is set when the executor itself failed the task, for example image_pull_failed.
	Reason string
}

// Status of work by external reference.
type Status int

// Status values.
const (
	StatusUnknown Status = iota
	StatusRunning
	StatusGone
)

// ErrUnknownRef is returned for an external reference that the executor does not know.
var ErrUnknownRef = errors.New("executor: unknown external reference")

// ReasonError is a start error with a task reason, for example image_pull_failed.
type ReasonError struct {
	Reason string
	Err    error
}

func (e *ReasonError) Error() string { return e.Reason + ": " + e.Err.Error() }

// Unwrap returns the cause.
func (e *ReasonError) Unwrap() error { return e.Err }

// Executor is the interface of all executors (REQ-EXR-001).
type Executor interface {
	// Type returns the executor type.
	Type() string
	// Start starts the work and returns its external reference.
	Start(ctx context.Context, t Task) (string, error)
	// Wait blocks until the work ends.
	Wait(ctx context.Context, ref string) Result
	// Cancel stops the work.
	Cancel(ctx context.Context, ref string) error
	// Status reports whether the work still exists.
	Status(ctx context.Context, ref string) (Status, error)
}
