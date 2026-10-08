//go:build e2e

package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTaskFanOut pins a task with each: one task run per item, outputs as a list in item
// order, one artifact file per item, a restart that runs only the failed item, the empty
// list, the item limit and max_parallel.
func TestTaskFanOut(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := adminClient(t, p)
	gate := filepath.Join(t.TempDir(), "gate")
	saveFiles(t, c, "fan", map[string]string{
		// Item b emits no output rows. Each item emits the artifact part.txt.
		"load.sh": `echo "letter-$1"
echo "$1" > part.txt
echo '{"type":"artifact","path":"part.txt","name":"part.txt"}' >> "$SLUICE_OUTPUTS"
if [ "$1" != b ]; then echo "{\"type\":\"output\",\"key\":\"rows\",\"value\":$2}" >> "$SLUICE_OUTPUTS"; fi
`,
		"abc.flow.yaml": `id: abc
tasks:
  - id: load
    type: script
    file: load.sh
    each: ["a", "b", "c"]
    args: ["${{ item }}", "${{ item_index }}"]
  - id: sum
    type: command
    depends_on: [load]
    artifacts:
      - { from: load, name: part.txt, path: data/part.txt }
    command: ["sh", "-c", "echo rows=$ROWS; cat data/0/part.txt data/1/part.txt data/2/part.txt | tr '\\n' ','"]
    env:
      ROWS: ${{ tasks.load.outputs.rows }}
`,
		"retry.flow.yaml": `id: retry
tasks:
  - id: work
    type: command
    each: [0, 1, 2]
    command: ["sh", "-c", "echo run-item-${{ item }}; test ${{ item }} != 1 || test -f ` + gate + `"]
  - id: after
    type: command
    depends_on: [work]
    command: ["echo", "after"]
`,
		"list.flow.yaml": `id: list
inputs:
  - { id: tables, type: json }
tasks:
  - id: work
    type: command
    each: ${{ inputs.tables }}
    command: ["echo", "table-${{ item }}"]
  - id: after
    type: command
    depends_on: [work]
    command: ["echo", "done=${{ tasks.work.outputs.anything }}"]
`,
		"child.flow.yaml": `id: child
inputs:
  - { id: n, type: int }
tasks:
  - { id: t, type: command, command: ["echo", "child-${{ inputs.n }}"] }
outputs:
  n: ${{ inputs.n }}
`,
		"parent.flow.yaml": `id: parent
tasks:
  - id: kids
    type: subflow
    flow: fan/child
    each: [7, 8]
    inputs:
      n: "${{ item }}"
  - id: after
    type: command
    depends_on: [kids]
    command: ["echo", "kids=${{ tasks.kids.outputs.n }}"]
`,
		"slow.flow.yaml": `id: slow
tasks:
  - id: work
    type: command
    each: [1, 2, 3, 4, 5]
    max_parallel: 2
    command: ["sleep", "2"]
`,
	})

	t.Run("one task run per item, outputs as a list, one artifact file per item", func(t *testing.T) {
		d := waitTerminal(t, c, triggerFlow(t, c, "fan", "abc", nil, nil).ID, 90*time.Second)
		if d.State != "SUCCESS" || len(d.runs("load")) != 3 {
			t.Fatalf("%s %s, %d task runs of load", d.State, d.Error, len(d.runs("load")))
		}
		for i, tr := range d.runs("load") {
			if tr.ItemIndex != i || tr.Item != []string{"a", "b", "c"}[i] {
				t.Fatalf("task run %d of load: item_index %d, item %v", i, tr.ItemIndex, tr.Item)
			}
		}
		logs := allLogs(t, c, d.ID, "load")
		for _, l := range logs {
			if l.ItemIndex == nil || l.Text == "letter-"+[]string{"a", "b", "c"}[*l.ItemIndex] {
				continue
			}
			if strings.HasPrefix(l.Text, "letter-") {
				t.Fatalf("log line %q is on item %d", l.Text, *l.ItemIndex)
			}
		}
		sum := logText(allLogs(t, c, d.ID, "sum"))
		if !strings.Contains(sum, "rows=[0,null,2]") || !strings.Contains(sum, "a,b,c,") {
			t.Fatalf("sum:\n%s", sum)
		}
	})

	t.Run("a failed item fails the task, and a restart runs only that item", func(t *testing.T) {
		first := waitTerminal(t, c, triggerFlow(t, c, "fan", "retry", nil, nil).ID, 90*time.Second)
		work := first.runs("work")
		if first.State != "FAILED" || len(work) != 3 || work[0].State != "SUCCESS" || work[1].State != "FAILED" || work[2].State != "SUCCESS" {
			t.Fatalf("%s: work %+v", first.State, work)
		}
		if a := first.last("after"); a.State != "SKIPPED" || !strings.Contains(first.Error, "item 1") {
			t.Fatalf("after %s, error %q", a.State, first.Error)
		}
		if err := os.WriteFile(gate, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		var restarted execDetail
		c.do(t, http.MethodPost, "/api/v1/executions/"+first.ID+"/restart", nil, http.StatusCreated, &restarted)
		second := waitTerminal(t, c, restarted.ID, 90*time.Second)
		work = second.runs("work")
		if second.State != "SUCCESS" || len(work) != 3 || work[0].ReusedFromID == nil || work[1].ReusedFromID != nil || work[2].ReusedFromID == nil {
			t.Fatalf("restart %s %s: work %+v", second.State, second.Error, work)
		}
		logs := logText(allLogs(t, c, second.ID, "work"))
		if !strings.Contains(logs, "run-item-1") || strings.Contains(logs, "run-item-0") || strings.Contains(logs, "run-item-2") {
			t.Fatalf("restart ran other items:\n%s", logs)
		}
	})

	t.Run("an empty list ends the task SUCCESS and a later task runs", func(t *testing.T) {
		d := waitTerminal(t, c, triggerFlow(t, c, "fan", "list", map[string]any{"tables": []any{}}, nil).ID, 60*time.Second)
		work := d.runs("work")
		if d.State != "SUCCESS" || len(work) != 1 || work[0].Reason != "no_items" || work[0].Item != nil {
			t.Fatalf("%s %s: work %+v", d.State, d.Error, work)
		}
		if logs := logText(allLogs(t, c, d.ID, "after")); !strings.Contains(logs, "done=[]") {
			t.Fatalf("after:\n%s", logs)
		}
	})

	t.Run("a list above 1000 items fails the task and no item starts", func(t *testing.T) {
		d := waitTerminal(t, c, triggerFlow(t, c, "fan", "list", map[string]any{"tables": make([]int, 1001)}, nil).ID, 60*time.Second)
		work := d.runs("work")
		if d.State != "FAILED" || len(work) != 1 || work[0].Reason != "template_error" || !strings.Contains(work[0].Error, "1001") {
			t.Fatalf("%s: work %+v", d.State, work)
		}
		d = waitTerminal(t, c, triggerFlow(t, c, "fan", "list", map[string]any{"tables": "orders"}, nil).ID, 60*time.Second)
		if tr := d.last("work"); d.State != "FAILED" || tr.Reason != "template_error" {
			t.Fatalf("a value that is not a list: %s, work %s %q", d.State, tr.Reason, tr.Error)
		}
	})

	t.Run("a subflow task with each starts one child execution per item", func(t *testing.T) {
		d := waitTerminal(t, c, triggerFlow(t, c, "fan", "parent", nil, nil).ID, 90*time.Second)
		kids := d.runs("kids")
		if d.State != "SUCCESS" || len(kids) != 2 || kids[0].ChildExecutionID == nil || kids[1].ChildExecutionID == nil ||
			*kids[0].ChildExecutionID == *kids[1].ChildExecutionID {
			t.Fatalf("%s %s: kids %+v", d.State, d.Error, kids)
		}
		if logs := logText(allLogs(t, c, d.ID, "after")); !strings.Contains(logs, "kids=[7,8]") {
			t.Fatalf("after:\n%s", logs)
		}
	})

	t.Run("max_parallel limits the items that run at the same time", func(t *testing.T) {
		d := triggerFlow(t, c, "fan", "slow", nil, nil)
		most := 0
		for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(100 * time.Millisecond) {
			x := getExec(t, c, d.ID)
			n := 0
			for _, tr := range x.TaskRuns {
				if tr.State == "RUNNING" || tr.State == "QUEUED" {
					n++
				}
			}
			most = max(most, n)
			if terminal[x.State] {
				if x.State != "SUCCESS" || len(x.runs("work")) != 5 {
					t.Fatalf("%s %s", x.State, x.Error)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the execution did not end")
			}
		}
		if most != 2 {
			t.Fatalf("at most %d items were active, want 2", most)
		}
	})
}
