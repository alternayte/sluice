package execution

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/platform/dbq"
	"github.com/alternayte/sluice/internal/platform/masking"
	"github.com/alternayte/sluice/internal/runnerapi"
	"github.com/alternayte/sluice/internal/storage"
)

// storedLine is one line of a chunk or of the archive.
type storedLine struct {
	N      int64     `json:"n"`
	TS     time.Time `json:"ts"`
	Stream string    `json:"stream"`
	Text   string    `json:"text"`
}

func gzipLines(lines []storedLine) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	enc := json.NewEncoder(gz)
	enc.SetEscapeHTML(false)
	for _, l := range lines {
		if err := enc.Encode(l); err != nil {
			return nil, err
		}
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gunzipLines(b []byte) ([]storedLine, error) {
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	return decodeLines(gz)
}

func decodeLines(r io.Reader) ([]storedLine, error) {
	var out []storedLine
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var l storedLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, sc.Err()
}

// maskCache keeps maskers per task run so that each log batch does not resolve secrets again.
type maskCache struct {
	mu sync.Mutex
	m  map[uuid.UUID]maskEntry
}

type maskEntry struct {
	masker *masking.Masker
	at     time.Time
}

var masks = &maskCache{m: map[uuid.UUID]maskEntry{}}

// MaskerFor returns the masker of a task run with its secret values (REQ-RUN-005, SI-10).
func (e *Engine) MaskerFor(ctx context.Context, tr dbq.TaskRun) *masking.Masker {
	masks.mu.Lock()
	if ent, ok := masks.m[tr.ID]; ok && time.Since(ent.at) < 10*time.Minute {
		masks.mu.Unlock()
		return ent.masker
	}
	masks.mu.Unlock()
	var values []string
	if p, err := e.BuildPlan(ctx, tr); err == nil {
		values = p.MaskValues
	}
	m := masking.New(values)
	masks.mu.Lock()
	if len(masks.m) > 10000 {
		masks.m = map[uuid.UUID]maskEntry{}
	}
	masks.m[tr.ID] = maskEntry{masker: m, at: time.Now()}
	masks.mu.Unlock()
	return m
}

// IngestLogs stores one runner batch. It is idempotent per (task run, seq) (REQ-RUN-002).
func (e *Engine) IngestLogs(ctx context.Context, tr dbq.TaskRun, seq int, lines []runnerapi.LogLine) error {
	if len(lines) == 0 {
		return nil
	}
	m := e.MaskerFor(ctx, tr)
	return pgx.BeginFunc(ctx, e.Pool, func(tx pgx.Tx) error {
		q := dbq.New(tx)
		if _, err := q.LockTaskRun(ctx, tr.ID); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM log_chunks WHERE task_run_id = $1 AND seq = $2)", tr.ID, seq).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
		first, err := q.NextLogLine(ctx, tr.ID)
		if err != nil {
			return err
		}
		stored := make([]storedLine, len(lines))
		for i, l := range lines {
			stream := l.Stream
			if stream != "stdout" && stream != "stderr" {
				stream = "system"
			}
			ts := l.TS
			if ts.IsZero() {
				ts = e.Clock.Now()
			}
			stored[i] = storedLine{N: first + int64(i), TS: ts, Stream: stream, Text: m.String(l.Text)}
		}
		data, err := gzipLines(stored)
		if err != nil {
			return err
		}
		_, err = q.InsertLogChunk(ctx, dbq.InsertLogChunkParams{TaskRunID: tr.ID, ExecutionID: tr.ExecutionID, Seq: int32(seq),
			FirstLine: first, LineCount: int32(len(lines)), Data: data, CreatedAt: e.Clock.Now()})
		return err
	})
}

// SystemLog appends server lines to a task run log.
func (e *Engine) SystemLog(ctx context.Context, tr dbq.TaskRun, texts ...string) {
	var seq int
	if err := e.Pool.QueryRow(ctx, "SELECT coalesce(max(seq), 0) + 1 FROM log_chunks WHERE task_run_id = $1", tr.ID).Scan(&seq); err != nil {
		return
	}
	lines := make([]runnerapi.LogLine, len(texts))
	for i, t := range texts {
		lines[i] = runnerapi.LogLine{TS: e.Clock.Now(), Stream: "system", Text: t}
	}
	if err := e.IngestLogs(ctx, tr, seq+100000, lines); err != nil {
		e.Log.Warn("system log", "err", err)
	}
}

// ArchiveTaskLogs moves the chunks of an ended task run to storage (REQ-RUN-008).
func (e *Engine) ArchiveTaskLogs(ctx context.Context, taskRunID uuid.UUID) error {
	q := dbq.New(e.Pool)
	tr, err := q.GetTaskRun(ctx, taskRunID)
	if err != nil {
		return err
	}
	if !TaskTerminal(tr.State) {
		return nil
	}
	lines, err := e.chunkLines(ctx, tr.ID, 0)
	if err != nil {
		return err
	}
	if len(lines) > 0 {
		key := storage.LogKey(tr.ExecutionID.String(), tr.ID.String())
		// Merge with an archive from an earlier call.
		if old, err := e.archivedLines(ctx, tr); err == nil && len(old) > 0 {
			lines = mergeLines(old, lines)
		}
		data, err := gzipLines(lines)
		if err != nil {
			return err
		}
		if _, err := e.Store.Put(ctx, key, bytes.NewReader(data), "application/gzip"); err != nil {
			return err
		}
	}
	if err := q.DeleteLogChunks(ctx, tr.ID); err != nil {
		return err
	}
	var open int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM task_runs t WHERE t.execution_id = $1 AND (t.state NOT IN ('SUCCESS','FAILED','TIMED_OUT','CANCELLED','SKIPPED')
		OR EXISTS (SELECT 1 FROM log_chunks c WHERE c.task_run_id = t.id))`, tr.ExecutionID).Scan(&open); err == nil && open == 0 {
		_ = q.SetLogArchived(ctx, tr.ExecutionID)
	}
	return nil
}

func mergeLines(a, b []storedLine) []storedLine {
	seen := map[int64]bool{}
	var out []storedLine
	for _, l := range append(a, b...) {
		if !seen[l.N] {
			seen[l.N] = true
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].N < out[j].N })
	return out
}

// chunkLines reads lines after line number `after` from Postgres chunks.
func (e *Engine) chunkLines(ctx context.Context, taskRunID uuid.UUID, after int64) ([]storedLine, error) {
	chunks, err := dbq.New(e.Pool).ListLogChunks(ctx, dbq.ListLogChunksParams{TaskRunID: taskRunID, FirstLine: after})
	if err != nil {
		return nil, err
	}
	var out []storedLine
	for _, c := range chunks {
		ls, err := gunzipLines(c.Data)
		if err != nil {
			return nil, err
		}
		for _, l := range ls {
			if l.N > after {
				out = append(out, l)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].N < out[j].N })
	return out, nil
}

func (e *Engine) archivedLines(ctx context.Context, tr dbq.TaskRun) ([]storedLine, error) {
	r, err := e.Store.Get(ctx, storage.LogKey(tr.ExecutionID.String(), tr.ID.String()))
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return gunzipLines(b)
}

// TaskLines reads all lines of a task run after `after` from both sources (REQ-RUN-008).
func (e *Engine) TaskLines(ctx context.Context, tr dbq.TaskRun, after int64) ([]storedLine, error) {
	arch, err := e.archivedLines(ctx, tr)
	if err != nil {
		return nil, err
	}
	live, err := e.chunkLines(ctx, tr.ID, after)
	if err != nil {
		return nil, err
	}
	var out []storedLine
	for _, l := range arch {
		if l.N > after {
			out = append(out, l)
		}
	}
	return mergeLines(out, live), nil
}

// LogLine is one line with its task run.
type LogLine struct {
	TaskRunID uuid.UUID
	TaskKey   string
	Attempt   int
	storedLine
}

// ExecutionLines reads the lines of all task runs of an execution, in time order.
// positions maps task run IDs to the last line already read.
func (e *Engine) ExecutionLines(ctx context.Context, execID uuid.UUID, task string, positions map[uuid.UUID]int64) ([]LogLine, error) {
	runs, err := dbq.New(e.Pool).ListExecutionTaskRuns(ctx, execID)
	if err != nil {
		return nil, err
	}
	var out []LogLine
	for _, tr := range runs {
		if task != "" && tr.TaskKey != task {
			continue
		}
		ls, err := e.TaskLines(ctx, tr, positions[tr.ID])
		if err != nil {
			return nil, err
		}
		for _, l := range ls {
			out = append(out, LogLine{TaskRunID: tr.ID, TaskKey: tr.TaskKey, Attempt: int(tr.Attempt), storedLine: l})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].TS.Equal(out[j].TS) {
			return out[i].TS.Before(out[j].TS)
		}
		if out[i].TaskRunID != out[j].TaskRunID {
			return out[i].TaskRunID.String() < out[j].TaskRunID.String()
		}
		return out[i].N < out[j].N
	})
	return out, nil
}

// FilterLines keeps lines that contain search (case-insensitive).
func FilterLines(lines []LogLine, search string) []LogLine {
	if search == "" {
		return lines
	}
	s := strings.ToLower(search)
	out := lines[:0:0]
	for _, l := range lines {
		if strings.Contains(strings.ToLower(l.Text), s) {
			out = append(out, l)
		}
	}
	return out
}
