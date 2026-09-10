package execution

import (
	"errors"
	"testing"
)

func TestStateTransitionTables(t *testing.T) {
	allowedExec := map[[2]string]bool{
		{ExecQueued, ExecRunning}: true, {ExecQueued, ExecSkipped}: true, {ExecQueued, ExecCancelled}: true,
		{ExecRunning, ExecSuccess}: true, {ExecRunning, ExecFailed}: true, {ExecRunning, ExecTimedOut}: true, {ExecRunning, ExecCancelling}: true,
		{ExecCancelling, ExecCancelled}: true,
	}
	for _, from := range ExecutionStates {
		for _, to := range ExecutionStates {
			err := CheckExecution(from, to)
			if allowedExec[[2]string{from, to}] != (err == nil) {
				t.Errorf("execution %s -> %s: err=%v", from, to, err)
			}
			var te *TransitionError
			if err != nil && !errors.As(err, &te) {
				t.Errorf("error type %T", err)
			}
		}
	}
	allowedTask := map[[2]string]bool{
		{TaskPending, TaskQueued}: true, {TaskPending, TaskSkipped}: true, {TaskPending, TaskCancelled}: true,
		{TaskQueued, TaskRunning}: true, {TaskQueued, TaskCancelled}: true,
		{TaskRunning, TaskSuccess}: true, {TaskRunning, TaskFailed}: true, {TaskRunning, TaskTimedOut}: true, {TaskRunning, TaskCancelled}: true,
	}
	for _, from := range TaskStates {
		for _, to := range TaskStates {
			if allowedTask[[2]string{from, to}] != (CheckTask(from, to) == nil) {
				t.Errorf("task %s -> %s", from, to)
			}
		}
	}
	for _, s := range ExecutionStates {
		if ExecutionTerminal(s) != (s != ExecQueued && s != ExecRunning && s != ExecCancelling) {
			t.Errorf("terminal %s", s)
		}
	}
}
