package ai

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func manyLines(task string, n int) []LogLine {
	out := make([]LogLine, n)
	for i := range out {
		out[i] = LogLine{TaskKey: task, Attempt: 1, Line: int64(i + 1), Stream: "stdout", Text: fmt.Sprintf("line-%d", i+1)}
	}
	return out
}

func TestTriageContext(t *testing.T) {
	t.Run("SCN-AI-008 the context stays within the limit and keeps head and tail lines", func(t *testing.T) {
		id := uuid.New()
		d := TriageData{ExecutionID: id, Namespace: "ns", FlowID: "big", State: "FAILED", FailedTask: "t",
			FlowPath: "big.flow.yaml", FlowSource: strings.Repeat("# comment\n", 500), LastSuccessID: &id,
			Diff: strings.Repeat("+x\n", 1000), Logs: manyLines("t", 50000)}
		limit := 5000
		out := buildTriageContext(d, limit-len(triageSystem))
		if n := len(out) + len(triageSystem); n > limit {
			t.Fatalf("context has %d characters, limit %d", n, limit)
		}
		for _, want := range []string{"[t #1] line-1\n", "[t #50000] line-50000\n", "lines omitted", "Failed task: t"} {
			if !strings.Contains(out, want) {
				t.Fatalf("context lacks %q:\n%s", want, out)
			}
		}
	})

	t.Run("SCN-AI-008 short logs stay complete", func(t *testing.T) {
		d := TriageData{ExecutionID: uuid.New(), FailedTask: "t", Logs: manyLines("t", 10)}
		out := buildTriageContext(d, 120000)
		if strings.Contains(out, "omitted") || !strings.Contains(out, "[t #10] line-10\n") || !strings.Contains(out, "No earlier successful execution.") {
			t.Fatalf("context:\n%s", out)
		}
	})
}

func TestFilterEvidence(t *testing.T) {
	logs := []LogLine{{TaskKey: "load", Line: 1, Text: "starting"}, {TaskKey: "load", Line: 2, Text: "ERROR: disk full on /data"}}
	got := filterEvidence([]Evidence{
		{Task: "load", Line: 9, Text: "disk full on /data"},
		{Task: "load", Line: 1, Text: "invented line"},
		{Task: "other", Line: 2, Text: "starting"},
		{Task: "load", Line: 1, Text: "  "},
	}, logs)
	if len(got) != 1 || got[0].Line != 2 || got[0].Text != "disk full on /data" {
		t.Fatalf("SCN-AI-007 evidence %+v", got)
	}
}
