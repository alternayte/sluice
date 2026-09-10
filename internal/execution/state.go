// Package execution implements the execution engine, the state machine, the
// dispatcher and retention (REQ-EXE-*).
package execution

import "fmt"

// Execution states (§6.6).
const (
	ExecQueued     = "QUEUED"
	ExecRunning    = "RUNNING"
	ExecCancelling = "CANCELLING"
	ExecSuccess    = "SUCCESS"
	ExecFailed     = "FAILED"
	ExecTimedOut   = "TIMED_OUT"
	ExecCancelled  = "CANCELLED"
	ExecSkipped    = "SKIPPED"
)

// Task run states (§6.6).
const (
	TaskPending   = "PENDING"
	TaskQueued    = "QUEUED"
	TaskRunning   = "RUNNING"
	TaskSuccess   = "SUCCESS"
	TaskFailed    = "FAILED"
	TaskTimedOut  = "TIMED_OUT"
	TaskCancelled = "CANCELLED"
	TaskSkipped   = "SKIPPED"
)

// Reasons of task runs and executions.
const (
	ReasonRunIfNotMet       = "run_if_not_met"
	ReasonUpstreamFailed    = "upstream_failed"
	ReasonInstanceShutdown  = "instance_shutdown"
	ReasonLost              = "lost"
	ReasonTemplateError     = "template_error"
	ReasonOutputError       = "output_error"
	ReasonDepthExceeded     = "depth_exceeded"
	ReasonSecretNotFound    = "secret_not_found"
	ReasonRuntimeNotFound   = "runtime_not_found"
	ReasonNoInstanceForPool = "no_instance_for_pool"
	ReasonImagePullFailed   = "image_pull_failed"
	ReasonPodPendingTimeout = "pod_pending_timeout"
	ReasonTimeout           = "timeout"
	ReasonCancelled         = "cancelled"
	ReasonExitCode          = "exit_code"
	ReasonHTTPStatus        = "http_status"
	ReasonChildFailed       = "child_failed"
	ReasonExecutor          = "executor_error"
)

// ExecutionStates lists all execution states.
var ExecutionStates = []string{ExecQueued, ExecRunning, ExecCancelling, ExecSuccess, ExecFailed, ExecTimedOut, ExecCancelled, ExecSkipped}

// TaskStates lists all task run states.
var TaskStates = []string{TaskPending, TaskQueued, TaskRunning, TaskSuccess, TaskFailed, TaskTimedOut, TaskCancelled, TaskSkipped}

var execTransitions = map[string][]string{
	ExecQueued:     {ExecRunning, ExecSkipped, ExecCancelled},
	ExecRunning:    {ExecSuccess, ExecFailed, ExecTimedOut, ExecCancelling},
	ExecCancelling: {ExecCancelled},
}

var taskTransitions = map[string][]string{
	TaskPending: {TaskQueued, TaskSkipped, TaskCancelled},
	TaskQueued:  {TaskRunning, TaskCancelled},
	TaskRunning: {TaskSuccess, TaskFailed, TaskTimedOut, TaskCancelled},
}

// TransitionError is returned for a transition that §6.6 does not allow.
type TransitionError struct {
	Kind, From, To string
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("%s transition %s -> %s is not allowed", e.Kind, e.From, e.To)
}

func check(kind string, table map[string][]string, from, to string) error {
	for _, s := range table[from] {
		if s == to {
			return nil
		}
	}
	return &TransitionError{Kind: kind, From: from, To: to}
}

// CheckExecution validates an execution transition (REQ-EXE-002).
func CheckExecution(from, to string) error { return check("execution", execTransitions, from, to) }

// CheckTask validates a task run transition (REQ-EXE-002).
func CheckTask(from, to string) error { return check("task run", taskTransitions, from, to) }

// ExecutionTerminal reports whether an execution state is final.
func ExecutionTerminal(s string) bool {
	switch s {
	case ExecSuccess, ExecFailed, ExecTimedOut, ExecCancelled, ExecSkipped:
		return true
	}
	return false
}

// TaskTerminal reports whether a task run state is final.
func TaskTerminal(s string) bool {
	switch s {
	case TaskSuccess, TaskFailed, TaskTimedOut, TaskCancelled, TaskSkipped:
		return true
	}
	return false
}
