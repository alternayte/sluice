package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/execution"
	"github.com/alternayte/sluice/internal/namespace"
)

// Exit codes of the client commands. The end state of an execution has its own code, so a
// script or a CI job can branch on it without parsing text.
const (
	exitExecFailed    = 10
	exitExecTimedOut  = 11
	exitExecCancelled = 12
	exitExecSkipped   = 13
	exitWaitTimeout   = 14
)

// ExitCode documents one exit code of the CLI. The usage text and the reference docs read ExitCodes.
type ExitCode struct {
	Code    int
	Meaning string
}

// ExitCodes are the exit codes of the CLI.
var ExitCodes = []ExitCode{
	{exitOK, "Success. With --wait: the execution ended SUCCESS."},
	{exitFail, "An API or network error. `sluice validate`: at least one file is invalid."},
	{exitConfig, "A usage or configuration error, for example a missing SLUICE_URL or SLUICE_TOKEN."},
	{exitExecFailed, "With --wait: the execution ended FAILED."},
	{exitExecTimedOut, "With --wait: the execution ended TIMED_OUT."},
	{exitExecCancelled, "With --wait: the execution ended CANCELLED."},
	{exitExecSkipped, "With --wait: the execution ended SKIPPED, for example by a concurrency limit with behavior skip."},
	{exitWaitTimeout, "With --wait: --timeout ended the wait. The execution continues."},
}

// stateExit maps an end state to its exit code.
func stateExit(state string) int {
	switch state {
	case execution.ExecSuccess:
		return exitOK
	case execution.ExecFailed:
		return exitExecFailed
	case execution.ExecTimedOut:
		return exitExecTimedOut
	case execution.ExecCancelled:
		return exitExecCancelled
	case execution.ExecSkipped:
		return exitExecSkipped
	}
	return exitFail
}

// multiFlag collects a repeated flag, for example --input a=1 --input b=2.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// clientCmd holds the shared flags of a client command.
type clientCmd struct {
	fs     *flag.FlagSet
	output *string
	stdout io.Writer
	stderr io.Writer
}

func newClientCmd(name, usage string, stdout, stderr io.Writer) *clientCmd {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: sluice %s\n\nFlags:\n", usage)
		fs.PrintDefaults()
		fmt.Fprintln(stderr, "\nThe command reads SLUICE_URL and SLUICE_TOKEN.")
	}
	c := &clientCmd{fs: fs, stdout: stdout, stderr: stderr}
	c.output = fs.String("output", "text", "output format: text or json")
	fs.StringVar(c.output, "o", "text", "short for --output")
	return c
}

// parse parses flags before and after the positional arguments and checks their number.
func (c *clientCmd) parse(args []string, positional int) ([]string, int) {
	var pos []string
	rest := args
	for {
		if err := c.fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, exitOK
			}
			return nil, exitConfig
		}
		rest = c.fs.Args()
		if len(rest) == 0 {
			break
		}
		pos = append(pos, rest[0])
		rest = rest[1:]
	}
	if len(pos) != positional {
		c.fs.Usage()
		return nil, exitConfig
	}
	if *c.output != "text" && *c.output != "json" {
		fmt.Fprintln(c.stderr, "--output must be text or json")
		return nil, exitConfig
	}
	return pos, -1
}

func (c *clientCmd) json() bool { return *c.output == "json" }

// printJSON writes raw API JSON, indented.
func (c *clientCmd) printJSON(v any) {
	enc := json.NewEncoder(c.stdout)
	enc.SetIndent("", "  ")
	if raw, ok := v.(json.RawMessage); ok {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			_ = enc.Encode(v)
			return
		}
	}
	_ = enc.Encode(v)
}

// fail prints an error and returns its exit code: 2 for configuration, 1 for API and network errors.
func (c *clientCmd) fail(err error) int {
	var ae *apiError
	switch {
	case errors.Is(err, errRemoteConfig):
		fmt.Fprintln(c.stderr, "error:", err)
		return exitConfig
	case errors.As(err, &ae):
		fmt.Fprintf(c.stderr, "error: %s: %s\n", ae.Code, ae.Message)
		for _, l := range ae.detailLines() {
			fmt.Fprintln(c.stderr, "  "+l)
		}
		if c.json() {
			c.printJSON(ae.envelope())
		}
	default:
		fmt.Fprintln(c.stderr, "error:", err)
	}
	return exitFail
}

func (c *clientCmd) remote() (*remote, int) {
	r, err := newRemote()
	if err != nil {
		return nil, c.fail(err)
	}
	return r, -1
}

func splitFlowRef(ref string) (string, string, error) {
	i := strings.LastIndex(ref, "/")
	if i <= 0 || i == len(ref)-1 {
		return "", "", fmt.Errorf("%q is not <namespace>/<flow>", ref)
	}
	return ref[:i], ref[i+1:], nil
}

// parseInputs turns k=v pairs into inputs. types maps an input ID to its declared type. The
// value of a string or select input stays a string, so 1, true and null are text there. For
// each other input, a value that is valid JSON keeps its JSON type (7, true, {"a":1}) and
// any other value is a string.
func parseInputs(pairs []string, types map[string]string) (map[string]any, error) {
	out := map[string]any{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--input %q must have the form key=value", p)
		}
		var j any
		if t := types[k]; t == "string" || t == "select" {
			out[k] = v
		} else if json.Unmarshal([]byte(v), &j) == nil {
			out[k] = j
		} else {
			out[k] = v
		}
	}
	return out, nil
}

func parseLabels(pairs []string) (map[string]string, error) {
	out := map[string]string{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--label %q must have the form key=value", p)
		}
		out[k] = v
	}
	return out, nil
}

func fmtDuration(ms *int64) string {
	if ms == nil {
		return "—"
	}
	d := time.Duration(*ms) * time.Millisecond
	if d < time.Second {
		return d.String()
	}
	return d.Round(100 * time.Millisecond).String()
}

func flowTitle(e execution.ExecutionSummary) string {
	if e.FlowID != nil {
		return e.Namespace + "/" + *e.FlowID
	}
	return e.Namespace + " (file run)"
}

func (r *remote) executionURL(id string) string { return r.base + "/executions/" + id }

// runRun implements `sluice run <namespace>/<flow>`.
func runRun(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newClientCmd("run", "run <namespace>/<flow> [--input k=v]... [--label k=v]... [--wait] [--timeout 10m] [--output json]", stdout, stderr)
	var inputs, labels multiFlag
	c.fs.Var(&inputs, "input", "an input as key=value; repeat for more. A string or select input takes the value as text. For other inputs a JSON value keeps its type.")
	c.fs.Var(&labels, "label", "a label as key=value; repeat for more")
	wait := c.fs.Bool("wait", false, "wait for the end, stream the logs to stderr, and exit with the code of the end state")
	timeout := c.fs.Duration("timeout", 0, "with --wait: stop waiting after this time and exit 14 (the execution continues)")
	pos, code := c.parse(args, 1)
	if code >= 0 {
		return code
	}
	ns, flowID, err := splitFlowRef(pos[0])
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitConfig
	}
	// Check the form of the pairs before any request.
	_, err = parseInputs(inputs, nil)
	if err == nil {
		var lb map[string]string
		lb, err = parseLabels(labels)
		if err == nil {
			r, code := c.remote()
			if code >= 0 {
				return code
			}
			flowPath := "/api/v1/flows/" + url.PathEscape(ns) + "/" + url.PathEscape(flowID)
			var types map[string]string
			if len(inputs) > 0 {
				if types, err = r.inputTypes(ctx, flowPath); err != nil {
					return c.fail(err)
				}
			}
			in, _ := parseInputs(inputs, types)
			var started execution.ExecutionDetail
			path := flowPath + "/executions"
			if err := r.do(ctx, http.MethodPost, path, map[string]any{"inputs": in, "labels": lb}, &started); err != nil {
				return c.fail(err)
			}
			if !*wait {
				if c.json() {
					c.printJSON(started)
				} else {
					fmt.Fprintf(stdout, "%s %s started\n%s\n", started.ID, flowTitle(started.ExecutionSummary), r.executionURL(started.ID.String()))
				}
				return exitOK
			}
			return c.wait(ctx, r, started.ID.String(), *timeout)
		}
	}
	fmt.Fprintln(stderr, "error:", err)
	return exitConfig
}

// inputTypes returns the declared type of each input of a flow. A flow without a valid
// revision gives no types: the run request then reports the flow.
func (r *remote) inputTypes(ctx context.Context, flowPath string) (map[string]string, error) {
	var f struct {
		Revision *struct {
			Definition *struct {
				Inputs []struct {
					ID   string `json:"id"`
					Type string `json:"type"`
				} `json:"inputs"`
			} `json:"definition"`
		} `json:"revision"`
	}
	if err := r.do(ctx, http.MethodGet, flowPath, nil, &f); err != nil {
		return nil, err
	}
	types := map[string]string{}
	if f.Revision != nil && f.Revision.Definition != nil {
		for _, in := range f.Revision.Definition.Inputs {
			types[in.ID] = in.Type
		}
	}
	return types, nil
}

// wait streams the logs of an execution to stderr until it ends, then prints the result.
func (c *clientCmd) wait(ctx context.Context, r *remote, id string, timeout time.Duration) int {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	fmt.Fprintf(c.stderr, "waiting for %s\n", r.executionURL(id))
	logsDone := make(chan error, 1)
	go func() {
		logsDone <- r.stream(ctx, "/api/v1/executions/"+id+"/logs/stream", func(ev sseEvent) bool {
			if ev.Event != "line" {
				return true
			}
			var l struct {
				TaskKey   string `json:"task_key"`
				Attempt   int    `json:"attempt"`
				ItemIndex *int   `json:"item_index"`
				Text      string `json:"text"`
			}
			if json.Unmarshal([]byte(ev.Data), &l) == nil {
				fmt.Fprintf(c.stderr, "[%s#%d] %s\n", execution.TaskLabel(l.TaskKey, l.ItemIndex), l.Attempt, l.Text)
			}
			return true
		})
	}()

	var d execution.ExecutionDetail
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		err := r.do(ctx, http.MethodGet, "/api/v1/executions/"+id, nil, &d)
		if err == nil && execution.ExecutionTerminal(d.State) {
			break
		}
		if err != nil && ctx.Err() == nil {
			var ae *apiError
			if errors.As(err, &ae) {
				return c.fail(err)
			}
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				fmt.Fprintf(c.stderr, "error: the execution did not end within %s; it continues to run\n", timeout)
				// The last known state goes to stdout, so a script still gets the execution ID.
				if d.ID != uuid.Nil {
					if c.json() {
						c.printJSON(d)
					} else {
						fmt.Fprintf(c.stdout, "%s %s %s\n%s\n", d.State, flowTitle(d.ExecutionSummary), d.ID, r.executionURL(id))
					}
				}
				return exitWaitTimeout
			}
			return exitFail
		case <-tick.C:
		}
	}
	// The log stream ends by itself after the last line of an ended execution.
	select {
	case <-logsDone:
	case <-time.After(5 * time.Second):
	}
	if c.json() {
		c.printJSON(d)
	} else {
		fmt.Fprintf(c.stdout, "%s %s %s in %s\n%s\n", d.State, flowTitle(d.ExecutionSummary), d.ID, fmtDuration(d.DurationMs), r.executionURL(id))
		if d.Error != "" {
			fmt.Fprintln(c.stdout, d.Error)
		}
	}
	return stateExit(d.State)
}

// runExecutionsList implements `sluice executions list`.
func runExecutionsList(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newClientCmd("executions list", "executions list [--namespace ns] [--flow ns/flow] [--state FAILED,...] [--waiting] [--limit 20]", stdout, stderr)
	ns := c.fs.String("namespace", "", "namespace and its children")
	flw := c.fs.String("flow", "", "flow as <namespace>/<flow>")
	state := c.fs.String("state", "", "comma-separated states, for example FAILED,TIMED_OUT")
	waiting := c.fs.Bool("waiting", false, "only executions with a task that waits for an answer")
	limit := c.fs.Int("limit", 20, "number of executions, 1 to 200")
	if _, code := c.parse(args, 0); code >= 0 {
		return code
	}
	r, code := c.remote()
	if code >= 0 {
		return code
	}
	q := url.Values{}
	for k, v := range map[string]string{"namespace": *ns, "flow": *flw, "state": strings.ToUpper(*state)} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if *waiting {
		q.Set("waiting", "true")
	}
	q.Set("limit", strconv.Itoa(*limit))
	var raw json.RawMessage
	if err := r.do(ctx, http.MethodGet, "/api/v1/executions?"+q.Encode(), nil, &raw); err != nil {
		return c.fail(err)
	}
	if c.json() {
		c.printJSON(raw)
		return exitOK
	}
	var list execution.ExecutionList
	if err := json.Unmarshal(raw, &list); err != nil {
		return c.fail(err)
	}
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tFLOW\tTRIGGER\tCREATED\tDURATION")
	for _, e := range list.Items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", e.ID, e.State, flowTitle(e), e.TriggerType, e.CreatedAt.Local().Format(time.DateTime), fmtDuration(e.DurationMs))
	}
	_ = tw.Flush()
	return exitOK
}

// runExecutionsGet implements `sluice executions get <id>`.
func runExecutionsGet(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newClientCmd("executions get", "executions get <execution-id>", stdout, stderr)
	pos, code := c.parse(args, 1)
	if code >= 0 {
		return code
	}
	r, code := c.remote()
	if code >= 0 {
		return code
	}
	var raw json.RawMessage
	if err := r.do(ctx, http.MethodGet, "/api/v1/executions/"+url.PathEscape(pos[0]), nil, &raw); err != nil {
		return c.fail(err)
	}
	if c.json() {
		c.printJSON(raw)
		return exitOK
	}
	var d execution.ExecutionDetail
	if err := json.Unmarshal(raw, &d); err != nil {
		return c.fail(err)
	}
	fmt.Fprintf(stdout, "%s %s %s\n", d.State, flowTitle(d.ExecutionSummary), d.ID)
	fmt.Fprintf(stdout, "trigger:  %s\ncreated:  %s\nduration: %s\n", d.TriggerType, d.CreatedAt.Local().Format(time.DateTime), fmtDuration(d.DurationMs))
	if d.Reason != "" {
		fmt.Fprintf(stdout, "reason:   %s\n", d.Reason)
	}
	if d.Error != "" {
		fmt.Fprintf(stdout, "error:    %s\n", d.Error)
	}
	fmt.Fprintf(stdout, "url:      %s\n\n", r.executionURL(d.ID.String()))
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "TASK\tATTEMPT\tSTATE\tREASON\tDURATION\tEXIT\tERROR")
	for _, t := range d.TaskRuns {
		// An item of a task with each shows as task[index] with its value after the error.
		if t.Item != nil {
			t.TaskKey = execution.TaskLabel(t.TaskKey, &t.ItemIndex)
			if b, err := json.Marshal(*t.Item); err == nil {
				t.Error = strings.TrimSpace(t.Error + " item=" + string(b))
			}
		}
		exit := "—"
		if t.ExitCode != nil {
			exit = strconv.Itoa(*t.ExitCode)
		}
		reason := t.Reason
		if reason == "" {
			reason = "—"
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\t%s\n", t.TaskKey, t.Attempt, t.State, reason, fmtDuration(t.DurationMs), exit, t.Error)
	}
	_ = tw.Flush()
	// A waiting task shows its question, so a person or an agent can answer it.
	for _, t := range d.TaskRuns {
		if t.State != execution.TaskWaiting || t.Wait == nil {
			continue
		}
		fmt.Fprintf(stdout, "\n%s waits for an answer", t.TaskKey)
		if t.Wait.Message != "" {
			fmt.Fprintf(stdout, ": %s", t.Wait.Message)
		}
		fmt.Fprintln(stdout)
		for _, f := range t.Wait.Fields {
			req := ""
			if f.Required {
				req = ", required"
			}
			fmt.Fprintf(stdout, "  --input %s=<%s%s>\n", f.ID, f.Type, req)
		}
		fmt.Fprintf(stdout, "  sluice executions resume %s --task %s\n  sluice executions reject %s --task %s --message <text>\n", d.ID, t.TaskKey, d.ID, t.TaskKey)
	}
	return exitOK
}

type logEntry struct {
	TaskKey   string `json:"task_key"`
	Attempt   int    `json:"attempt"`
	ItemIndex *int   `json:"item_index"`
	N         int64  `json:"n"`
	Stream    string `json:"stream"`
	Text      string `json:"text"`
	TS        string `json:"ts"`
}

// runExecutionsLogs implements `sluice executions logs <id>`.
func runExecutionsLogs(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newClientCmd("executions logs", "executions logs <execution-id> [--task name] [--follow]", stdout, stderr)
	task := c.fs.String("task", "", "only the lines of this task")
	follow := c.fs.Bool("follow", false, "keep printing new lines until the execution ends (text output only)")
	pos, code := c.parse(args, 1)
	if code >= 0 {
		return code
	}
	r, code := c.remote()
	if code >= 0 {
		return code
	}
	id := url.PathEscape(pos[0])
	taskQ := ""
	if *task != "" {
		taskQ = "&task=" + url.QueryEscape(*task)
	}
	printLine := func(l logEntry) {
		if *task != "" && l.ItemIndex == nil {
			fmt.Fprintln(stdout, l.Text)
			return
		}
		fmt.Fprintf(stdout, "[%s#%d] %s\n", execution.TaskLabel(l.TaskKey, l.ItemIndex), l.Attempt, l.Text)
	}
	if *follow && !c.json() {
		err := r.stream(ctx, "/api/v1/executions/"+id+"/logs/stream?"+strings.TrimPrefix(taskQ, "&"), func(ev sseEvent) bool {
			var l logEntry
			if ev.Event == "line" && json.Unmarshal([]byte(ev.Data), &l) == nil {
				printLine(l)
			}
			return true
		})
		if err != nil {
			return c.fail(err)
		}
		return exitOK
	}
	all := []logEntry{}
	cursor := ""
	for {
		var page struct {
			Lines      []logEntry `json:"lines"`
			NextCursor *string    `json:"next_cursor"`
		}
		path := "/api/v1/executions/" + id + "/logs?limit=5000" + taskQ
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		if err := r.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			return c.fail(err)
		}
		all = append(all, page.Lines...)
		if page.NextCursor == nil || *page.NextCursor == "" || len(page.Lines) < 5000 {
			break
		}
		cursor = *page.NextCursor
	}
	if c.json() {
		c.printJSON(all)
		return exitOK
	}
	for _, l := range all {
		printLine(l)
	}
	return exitOK
}

// runExecutionAction implements cancel, rerun and restart.
func runExecutionAction(action string) func(context.Context, []string, io.Writer, io.Writer) int {
	return func(ctx context.Context, args []string, stdout, stderr io.Writer) int {
		c := newClientCmd("executions "+action, "executions "+action+" <execution-id> [--wait] [--timeout 10m]", stdout, stderr)
		var wait *bool
		var timeout *time.Duration
		if action != "cancel" {
			wait = c.fs.Bool("wait", false, "wait for the new execution to end, and exit with the code of its end state")
			timeout = c.fs.Duration("timeout", 0, "with --wait: stop waiting after this time and exit 14")
		}
		pos, code := c.parse(args, 1)
		if code >= 0 {
			return code
		}
		r, code := c.remote()
		if code >= 0 {
			return code
		}
		var d execution.ExecutionDetail
		if err := r.do(ctx, http.MethodPost, "/api/v1/executions/"+url.PathEscape(pos[0])+"/"+action, nil, &d); err != nil {
			return c.fail(err)
		}
		if wait != nil && *wait {
			return c.wait(ctx, r, d.ID.String(), *timeout)
		}
		if c.json() {
			c.printJSON(d)
			return exitOK
		}
		if action == "cancel" {
			fmt.Fprintf(stdout, "%s %s\n", d.State, d.ID)
		} else {
			fmt.Fprintf(stdout, "%s %s started\n%s\n", d.ID, flowTitle(d.ExecutionSummary), r.executionURL(d.ID.String()))
		}
		return exitOK
	}
}

// runExecutionsResume implements `sluice executions resume <id> --task <task>`: the answer
// to a waiting task. Each --input converts by the declared type of its field.
func runExecutionsResume(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newClientCmd("executions resume", "executions resume <execution-id> --task <task> [--input k=v]...", stdout, stderr)
	task := c.fs.String("task", "", "the waiting task")
	var inputs multiFlag
	c.fs.Var(&inputs, "input", "a field of the answer as key=value; repeat for more. A string or select field takes the value as text.")
	pos, code := c.parse(args, 1)
	if code >= 0 {
		return code
	}
	if *task == "" {
		c.fs.Usage()
		return exitConfig
	}
	if _, err := parseInputs(inputs, nil); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitConfig
	}
	r, code := c.remote()
	if code >= 0 {
		return code
	}
	base := "/api/v1/executions/" + url.PathEscape(pos[0])
	types := map[string]string{}
	if len(inputs) > 0 {
		var d execution.ExecutionDetail
		if err := r.do(ctx, http.MethodGet, base, nil, &d); err != nil {
			return c.fail(err)
		}
		for _, tr := range d.TaskRuns {
			if tr.TaskKey == *task && tr.Wait != nil {
				for _, f := range tr.Wait.Fields {
					types[f.ID] = f.Type
				}
			}
		}
	}
	in, _ := parseInputs(inputs, types)
	return c.answer(ctx, r, base+"/tasks/"+url.PathEscape(*task)+"/resume", map[string]any{"inputs": in}, *task, "resumed")
}

// runExecutionsReject implements `sluice executions reject <id> --task <task>`.
func runExecutionsReject(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newClientCmd("executions reject", "executions reject <execution-id> --task <task> [--message text]", stdout, stderr)
	task := c.fs.String("task", "", "the waiting task")
	message := c.fs.String("message", "", "the reason; it becomes the error of the task run")
	pos, code := c.parse(args, 1)
	if code >= 0 {
		return code
	}
	if *task == "" {
		c.fs.Usage()
		return exitConfig
	}
	r, code := c.remote()
	if code >= 0 {
		return code
	}
	path := "/api/v1/executions/" + url.PathEscape(pos[0]) + "/tasks/" + url.PathEscape(*task) + "/reject"
	return c.answer(ctx, r, path, map[string]any{"message": *message}, *task, "rejected")
}

// answer sends a resume or a reject and prints the execution.
func (c *clientCmd) answer(ctx context.Context, r *remote, path string, body any, task, done string) int {
	var d execution.ExecutionDetail
	if err := r.do(ctx, http.MethodPost, path, body, &d); err != nil {
		return c.fail(err)
	}
	if c.json() {
		c.printJSON(d)
	} else {
		fmt.Fprintf(c.stdout, "%s %s of %s %s\n%s\n", done, task, flowTitle(d.ExecutionSummary), d.ID, r.executionURL(d.ID.String()))
	}
	return exitOK
}

// runFlowsList implements `sluice flows list`.
func runFlowsList(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newClientCmd("flows list", "flows list [--namespace ns]", stdout, stderr)
	ns := c.fs.String("namespace", "", "namespace and its children")
	if _, code := c.parse(args, 0); code >= 0 {
		return code
	}
	r, code := c.remote()
	if code >= 0 {
		return code
	}
	q := url.Values{"limit": {"200"}}
	if *ns != "" {
		q.Set("namespace", *ns)
	}
	var raw json.RawMessage
	if err := r.do(ctx, http.MethodGet, "/api/v1/flows?"+q.Encode(), nil, &raw); err != nil {
		return c.fail(err)
	}
	if c.json() {
		c.printJSON(raw)
		return exitOK
	}
	var list namespace.FlowList
	if err := json.Unmarshal(raw, &list); err != nil {
		return c.fail(err)
	}
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "FLOW\tVALID\tENABLED\tLAST STATE\tDESCRIPTION")
	for _, f := range list.Items {
		last := "—"
		if f.LastExecution != nil {
			last = f.LastExecution.State
		}
		fmt.Fprintf(tw, "%s/%s\t%t\t%t\t%s\t%s\n", f.Namespace, f.FlowID, f.Valid, !f.Disabled, last, f.Description)
	}
	_ = tw.Flush()
	return exitOK
}

// runFlowsGet implements `sluice flows get <namespace>/<flow>`.
func runFlowsGet(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newClientCmd("flows get", "flows get <namespace>/<flow>", stdout, stderr)
	pos, code := c.parse(args, 1)
	if code >= 0 {
		return code
	}
	ns, flowID, err := splitFlowRef(pos[0])
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitConfig
	}
	r, code := c.remote()
	if code >= 0 {
		return code
	}
	var raw json.RawMessage
	if err := r.do(ctx, http.MethodGet, "/api/v1/flows/"+url.PathEscape(ns)+"/"+url.PathEscape(flowID), nil, &raw); err != nil {
		return c.fail(err)
	}
	if c.json() {
		c.printJSON(raw)
		return exitOK
	}
	var f namespace.FlowDetail
	if err := json.Unmarshal(raw, &f); err != nil {
		return c.fail(err)
	}
	fmt.Fprintf(stdout, "%s/%s  valid=%t enabled=%t\npath: %s\n", f.Namespace, f.FlowID, f.Valid, !f.Disabled, f.Path)
	if f.Description != "" {
		fmt.Fprintln(stdout, f.Description)
	}
	if f.Revision != nil {
		fmt.Fprintf(stdout, "\n%s", f.Revision.Source)
	}
	return exitOK
}

// runNamespacesPush implements `sluice namespaces push <dir>`. It sends the files that differ
// from the head version as one new version, and creates no version when nothing differs.
func runNamespacesPush(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newClientCmd("namespaces push", "namespaces push <dir> [--namespace name] [--message text] [--create] [--verbose]", stdout, stderr)
	nsFlag := c.fs.String("namespace", "", "target namespace (default: the name of the directory)")
	message := c.fs.String("message", "", "version message (default: \"Push from the sluice CLI\")")
	create := c.fs.Bool("create", false, "create the namespace when it does not exist")
	verbose := c.fs.Bool("verbose", false, "print each path that an ignore rule skips")
	pos, code := c.parse(args, 1)
	if code >= 0 {
		return code
	}
	dir := pos[0]
	ns := *nsFlag
	if ns == "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return exitConfig
		}
		ns = filepath.Base(abs)
	}
	msg := *message
	if msg == "" {
		msg = "Push from the sluice CLI"
	}
	nd, err := readNamespaceDir(dir)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitConfig
	}
	nd.report(stderr, *verbose)
	if len(nd.Secrets) > 0 {
		// A version is immutable, so the push stops before it sends a file.
		fmt.Fprintln(stderr, "error: these files look like secrets, and nothing was pushed:")
		for _, p := range nd.Secrets {
			fmt.Fprintln(stderr, "  "+p)
		}
		fmt.Fprintln(stderr, "For each file, "+secretHelp+".")
		return exitFail
	}
	files := nd.Files
	r, code := c.remote()
	if code >= 0 {
		return code
	}

	nsPath := "/api/v1/namespaces/" + url.PathEscape(ns)
	var head struct {
		HeadVersion *int  `json:"head_version"`
		ReadOnly    *bool `json:"read_only"`
	}
	err = r.do(ctx, http.MethodGet, nsPath, nil, &head)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		if !*create {
			fmt.Fprintf(stderr, "error: namespace %s does not exist; use --create to create it\n", ns)
			return exitFail
		}
		if err := r.do(ctx, http.MethodPost, "/api/v1/namespaces", map[string]string{"name": ns}, nil); err != nil {
			return c.fail(err)
		}
		fmt.Fprintf(stderr, "created namespace %s\n", ns)
		err = r.do(ctx, http.MethodGet, nsPath, nil, &head)
	}
	if err != nil {
		return c.fail(err)
	}

	var remoteFiles namespace.FileList
	if err := r.do(ctx, http.MethodGet, nsPath+"/files", nil, &remoteFiles); err != nil {
		return c.fail(err)
	}
	have := map[string]namespace.FileEntry{}
	for _, f := range remoteFiles.Items {
		have[f.Path] = f
	}
	type change struct {
		Op         string `json:"op"`
		Path       string `json:"path"`
		Content    string `json:"content,omitempty"`
		Base64     string `json:"content_base64,omitempty"`
		Executable *bool  `json:"executable,omitempty"`
	}
	var changes []change
	var added, updated, deleted []string
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		b := files[p]
		st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return exitConfig
		}
		exec := st.Mode()&0o111 != 0
		old, ok := have[p]
		if ok && old.Hash == namespace.ContentHash(b) && old.Executable == exec {
			continue
		}
		ch := change{Op: "put", Path: p, Executable: &exec}
		if utf8.Valid(b) && !bytes.Contains(b, []byte{0}) {
			ch.Content = string(b)
		} else {
			ch.Base64 = base64.StdEncoding.EncodeToString(b)
		}
		changes = append(changes, ch)
		if ok {
			updated = append(updated, p)
		} else {
			added = append(added, p)
		}
	}
	for p := range have {
		if _, ok := files[p]; !ok {
			changes = append(changes, change{Op: "delete", Path: p})
			deleted = append(deleted, p)
		}
	}
	sort.Strings(deleted)

	result := map[string]any{"namespace": ns, "added": nonNil(added), "updated": nonNil(updated), "deleted": nonNil(deleted)}
	if len(changes) == 0 {
		result["version"] = head.HeadVersion
		result["changed"] = false
		if c.json() {
			c.printJSON(result)
		} else {
			fmt.Fprintf(stdout, "%s is up to date; no new version\n", ns)
		}
		return exitOK
	}
	body := map[string]any{"message": msg, "changes": changes}
	if head.HeadVersion != nil {
		body["base_version"] = *head.HeadVersion
	}
	var snap namespace.Snapshot
	if err := r.do(ctx, http.MethodPost, nsPath+"/changes", body, &snap); err != nil {
		return c.fail(err)
	}
	result["version"] = snap.Version
	result["changed"] = true
	if c.json() {
		c.printJSON(result)
		return exitOK
	}
	v := "?"
	if snap.Version != nil {
		v = strconv.Itoa(*snap.Version)
	}
	fmt.Fprintf(stdout, "%s version %s: %d added, %d updated, %d deleted\n", ns, v, len(added), len(updated), len(deleted))
	for _, p := range added {
		fmt.Fprintln(stdout, "  + "+p)
	}
	for _, p := range updated {
		fmt.Fprintln(stdout, "  ~ "+p)
	}
	for _, p := range deleted {
		fmt.Fprintln(stdout, "  - "+p)
	}
	return exitOK
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
