package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/ai/aidb"
	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// Triage log line limits of REQ-AI-007.
const (
	triageHeadLines = 50
	triageTailLines = 400
	triageDiffLines = 200
	// triageStale fails a running triage whose instance stopped.
	triageStale = 15 * time.Minute
	// triageTimeout limits one triage.
	triageTimeout = 3 * time.Minute
	triagePoll    = 2 * time.Second
)

// TriageData is the context of one triage (REQ-AI-007). The adapter masks all texts.
type TriageData struct {
	ExecutionID   uuid.UUID
	Namespace     string
	FlowID        string
	State         string
	Error         string
	FlowPath      string
	FlowSource    string
	FailedTask    string
	TaskSpec      json.RawMessage
	ExitCode      *int
	TaskError     string
	Outputs       json.RawMessage
	Metrics       json.RawMessage
	Logs          []LogLine
	LastSuccessID *uuid.UUID
	// Diff is the file diff against the snapshot of the last SUCCESS execution of the flow.
	Diff string
	// Durations are the durations in ms of the last 10 executions of the flow.
	Durations []int64
}

// Evidence is one log line that supports a triage.
type Evidence struct {
	Task string `json:"task"`
	Line int64  `json:"line"`
	Text string `json:"text"`
}

// Insight is one triage result.
type Insight struct {
	ID            uuid.UUID  `json:"id"`
	ExecutionID   uuid.UUID  `json:"execution_id"`
	Kind          string     `json:"kind" enum:"triage"`
	Status        string     `json:"status" enum:"pending,running,done,failed"`
	Summary       string     `json:"summary"`
	ProbableCause string     `json:"probable_cause"`
	Evidence      []Evidence `json:"evidence"`
	SuggestedFix  string     `json:"suggested_fix"`
	Confidence    string     `json:"confidence" enum:",low,medium,high"`
	Model         string     `json:"model"`
	Error         string     `json:"error"`
	CreatedAt     time.Time  `json:"created_at"`
}

func insightOut(r aidb.AiInsight) Insight {
	in := Insight{ID: r.ID, ExecutionID: r.ExecutionID, Kind: r.Kind, Status: r.Status, Summary: r.Summary, ProbableCause: r.ProbableCause,
		SuggestedFix: r.SuggestedFix, Confidence: r.Confidence, Model: r.Model, Error: r.Error, CreatedAt: r.CreatedAt, Evidence: []Evidence{}}
	_ = json.Unmarshal(r.Evidence, &in.Evidence)
	if in.Evidence == nil {
		in.Evidence = []Evidence{}
	}
	return in
}

// Insights lists the insights of an execution, newest first.
func (s *Service) Insights(ctx context.Context, execID uuid.UUID) ([]Insight, error) {
	rows, err := aidb.New(s.Pool).ListInsights(ctx, execID)
	if err != nil {
		return nil, err
	}
	out := make([]Insight, 0, len(rows))
	for _, r := range rows {
		out = append(out, insightOut(r))
	}
	return out, nil
}

// failedState reports whether an execution state gets a triage.
func failedState(state string) bool { return state == "FAILED" || state == "TIMED_OUT" }

// RequestTriage queues a triage of a failed execution on demand. A queued or running
// triage of the same execution is not duplicated.
func (s *Service) RequestTriage(ctx context.Context, execID uuid.UUID, state string) error {
	if cfg, err := s.Provider(ctx); err != nil {
		return err
	} else if cfg == nil {
		return ErrDisabled
	}
	if !failedState(state) {
		return httpx.Errorf(http.StatusConflict, "not_failed", "only FAILED and TIMED_OUT executions get a triage")
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := aidb.New(tx)
		// Serialize requests of one execution.
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", "ai-triage:"+execID.String()); err != nil {
			return err
		}
		active, err := q.ActiveInsightExists(ctx, execID)
		if err != nil || active {
			return err
		}
		id, _ := uuid.NewV7()
		if err := q.InsertInsight(ctx, aidb.InsertInsightParams{ID: id, ExecutionID: execID, CreatedAt: s.Clock.Now()}); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "ai.triage.request", TargetType: "execution", TargetID: execID.String()})
	})
}

// OnEnd queues an automatic triage in the transaction that ends an execution, when auto
// triage is on (REQ-AI-007). It runs in a savepoint and never fails the end.
func (s *Service) OnEnd(ctx context.Context, tx pgx.Tx, execID uuid.UUID, state string) error {
	if !failedState(state) {
		return nil
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	err = func() error {
		b, err := aidb.New(sp).GetSetting(ctx, providerSetting)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var cfg ProviderConfig
		if err := json.Unmarshal(b, &cfg); err != nil || !cfg.AutoTriage {
			return err
		}
		id, _ := uuid.NewV7()
		return aidb.New(sp).InsertInsight(ctx, aidb.InsertInsightParams{ID: id, ExecutionID: execID, CreatedAt: s.Clock.Now()})
	}()
	if err != nil {
		_ = sp.Rollback(ctx)
		if s.Log != nil {
			s.Log.Warn("queue automatic triage", "execution", execID, "err", err)
		}
		return nil
	}
	return sp.Commit(ctx)
}

// RunTriage processes queued triages until ctx ends. Every instance runs it; SKIP LOCKED
// gives each triage to one instance.
func (s *Service) RunTriage(ctx context.Context) {
	t := time.NewTicker(triagePoll)
	defer t.Stop()
	for {
		if _, err := aidb.New(s.Pool).FailStaleInsights(ctx, s.Clock.Now().Add(-triageStale)); err != nil && ctx.Err() == nil {
			s.Log.Warn("fail stale triages", "err", err)
		}
		for ctx.Err() == nil {
			row, err := aidb.New(s.Pool).ClaimInsight(ctx)
			if errors.Is(err, pgx.ErrNoRows) {
				break
			}
			if err != nil {
				if ctx.Err() == nil {
					s.Log.Warn("claim triage", "err", err)
				}
				break
			}
			s.runOne(ctx, row)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// triageSchema is the result schema of REQ-AI-007.
var triageSchema = json.RawMessage(`{"type":"object","properties":{
"summary":{"type":"string"},
"probable_cause":{"type":"string"},
"evidence":{"type":"array","items":{"type":"object","properties":{"task":{"type":"string"},"line":{"type":"integer"},"text":{"type":"string"}},"required":["task","line","text"]}},
"suggested_fix":{"type":"string"},
"confidence":{"type":"string","enum":["low","medium","high"]}},
"required":["summary","probable_cause","evidence","suggested_fix","confidence"]}`)

const triageSystem = "You triage failed executions of the Sluice orchestrator. Use only the given context. " +
	"Quote evidence log lines exactly as they appear, with their task key and line number. Keep the answer short."

func (s *Service) runOne(ctx context.Context, row aidb.AiInsight) {
	ctx, cancel := context.WithTimeout(ctx, triageTimeout)
	defer cancel()
	finish := func(p aidb.FinishInsightParams) {
		p.ID = row.ID
		if p.Evidence == nil {
			p.Evidence = json.RawMessage(`[]`)
		}
		if err := aidb.New(s.Pool).FinishInsight(context.WithoutCancel(ctx), p); err != nil {
			s.Log.Warn("store triage", "insight", row.ID, "err", err)
		}
	}
	fail := func(err error) {
		finish(aidb.FinishInsightParams{Status: "failed", Error: err.Error()})
	}
	m, _, err := s.Model(ctx)
	if err != nil {
		fail(err)
		return
	}
	data, err := s.Data.Triage(ctx, row.ExecutionID)
	if err != nil {
		fail(err)
		return
	}
	user := buildTriageContext(data, s.maxContext()-len(triageSystem))
	resp, err := m.Complete(ctx, Request{System: triageSystem, JSONSchema: triageSchema, JSONName: "triage",
		Messages: []Message{{Role: "user", Content: []Block{{Type: BlockText, Text: user}}}}}, nil)
	if err != nil {
		fail(err)
		return
	}
	var out struct {
		Summary       string     `json:"summary"`
		ProbableCause string     `json:"probable_cause"`
		Evidence      []Evidence `json:"evidence"`
		SuggestedFix  string     `json:"suggested_fix"`
		Confidence    string     `json:"confidence"`
	}
	if err := json.Unmarshal(resp.JSON, &out); err != nil {
		fail(fmt.Errorf("the model returned an invalid triage: %w", err))
		return
	}
	switch out.Confidence {
	case "low", "medium", "high":
	default:
		out.Confidence = "low"
	}
	ev, _ := json.Marshal(filterEvidence(out.Evidence, data.Logs))
	finish(aidb.FinishInsightParams{Status: "done", Summary: out.Summary, ProbableCause: out.ProbableCause, Evidence: ev,
		SuggestedFix: out.SuggestedFix, Confidence: out.Confidence, Model: m.Name()})
}

func (s *Service) maxContext() int {
	if s.MaxContextChars <= 0 {
		return 120000
	}
	return s.MaxContextChars
}

// filterEvidence removes evidence whose text is not in the logs (REQ-AI-007). A line number
// that does not match is corrected to the first line with the text.
func filterEvidence(ev []Evidence, logs []LogLine) []Evidence {
	out := []Evidence{}
	for _, e := range ev {
		text := strings.TrimSpace(e.Text)
		if text == "" {
			continue
		}
		for _, l := range logs {
			if (e.Task == "" || e.Task == l.TaskKey) && strings.Contains(l.Text, text) {
				out = append(out, Evidence{Task: l.TaskKey, Line: l.Line, Text: text})
				break
			}
		}
	}
	return out
}

func logText(l LogLine) string { return fmt.Sprintf("[%s #%d] %s", l.TaskKey, l.Line, l.Text) + "\n" }

// buildTriageContext renders the triage context in at most limit characters. The fixed
// sections come first; the diff and the flow source are cut when they are too long. The
// log section keeps the first 50 and the last 400 lines of the failed task, and cuts
// lines from the middle until the text fits (REQ-AI-008).
func buildTriageContext(d TriageData, limit int) string {
	if limit < 1000 {
		limit = 1000
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Execution %s of flow %s/%s ended %s.\n", d.ExecutionID, d.Namespace, d.FlowID, d.State)
	if d.Error != "" {
		fmt.Fprintf(&b, "Execution error: %s\n", d.Error)
	}
	fmt.Fprintf(&b, "Failed task: %s", d.FailedTask)
	if d.ExitCode != nil {
		fmt.Fprintf(&b, ", exit code %d", *d.ExitCode)
	}
	if d.TaskError != "" {
		fmt.Fprintf(&b, ", error: %s", d.TaskError)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "Task spec: %s\n", compactJSON(d.TaskSpec))
	fmt.Fprintf(&b, "Outputs: %s\nMetrics: %s\n", compactJSON(d.Outputs), compactJSON(d.Metrics))
	fmt.Fprintf(&b, "Durations of the last executions in ms: %v\n", d.Durations)
	fixed := b.String()

	diff := "No earlier successful execution.\n"
	if d.LastSuccessID != nil {
		lines := strings.SplitAfter(d.Diff, "\n")
		if len(lines) > triageDiffLines {
			lines = append(lines[:triageDiffLines], "…(diff cut)\n")
		}
		diff = fmt.Sprintf("Diff against the last successful execution %s:\n%s", d.LastSuccessID, strings.Join(lines, ""))
		if d.Diff == "" {
			diff = fmt.Sprintf("No file changes since the last successful execution %s.\n", d.LastSuccessID)
		}
	}
	source := fmt.Sprintf("Flow source (%s):\n%s\n", d.FlowPath, d.FlowSource)

	// The fixed part, the source and the diff get at most half of the limit together.
	half := limit / 2
	if len(fixed) > half {
		fixed = fixed[:half] + "…\n"
	}
	rest := half - len(fixed)
	if len(diff) > rest/2 {
		diff = cutText(diff, rest/2)
	}
	if len(source) > rest-len(diff) {
		source = cutText(source, rest-len(diff))
	}

	head, tail := d.Logs, []LogLine(nil)
	if len(d.Logs) > triageHeadLines+triageTailLines {
		head, tail = d.Logs[:triageHeadLines], d.Logs[len(d.Logs)-triageTailLines:]
	}
	header := fmt.Sprintf("Log lines of task %s (%d lines in total):\n", d.FailedTask, len(d.Logs))
	budget := limit - len(fixed) - len(source) - len(diff) - len(header) - 60
	logs := fitLogs(head, tail, len(d.Logs), budget)
	return fixed + source + diff + header + logs
}

func compactJSON(b json.RawMessage) string {
	if len(b) == 0 {
		return "{}"
	}
	return string(b)
}

// cutText cuts s to at most n characters with a marker.
func cutText(s string, n int) string {
	const marker = "…(cut)\n"
	if len(s) <= n {
		return s
	}
	if n <= len(marker) {
		return ""
	}
	return s[:n-len(marker)] + marker
}

// fitLogs renders head and tail lines in at most budget characters. When they do not fit,
// it keeps a quarter of the budget for the first head lines and the rest for the last tail
// lines, and marks the omitted lines.
func fitLogs(head, tail []LogLine, total, budget int) string {
	all := append(append([]LogLine(nil), head...), tail...)
	var full strings.Builder
	for _, l := range all {
		full.WriteString(logText(l))
	}
	if full.Len() <= budget && len(all) == total {
		return full.String()
	}
	if len(tail) == 0 {
		// Short logs that do not fit: split them into head and tail.
		n := len(head) / 4
		head, tail = head[:n], head[n:]
	}
	headBudget := budget / 4
	var hb strings.Builder
	used := 0
	for _, l := range head {
		t := logText(l)
		if hb.Len()+len(t) > headBudget {
			break
		}
		hb.WriteString(t)
		used++
	}
	tailBudget := budget - hb.Len()
	var kept []string
	size := 0
	for i := len(tail) - 1; i >= 0; i-- {
		t := logText(tail[i])
		if size+len(t) > tailBudget {
			break
		}
		kept = append(kept, t)
		size += len(t)
	}
	var b strings.Builder
	b.WriteString(hb.String())
	fmt.Fprintf(&b, "…(%d lines omitted)\n", total-used-len(kept))
	for i := len(kept) - 1; i >= 0; i-- {
		b.WriteString(kept[i])
	}
	return b.String()
}
