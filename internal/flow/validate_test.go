package flow

import (
	"strings"
	"testing"
)

// TestTriggerInputReferences checks that trigger inputs accept only trigger.<path>
// references, because a trigger renders its inputs over the trigger payload only.
func TestTriggerInputReferences(t *testing.T) {
	const head = "id: f\ninputs:\n  - { id: x, type: string }\ntriggers:\n  - id: t\n    type: webhook\n    inputs:\n      x: "
	const tail = "\ntasks:\n  - id: a\n    type: command\n    command: [\"true\"]\n"
	cases := map[string]bool{
		`"${{ trigger.body.x }}"`:                  true,
		`"${{ trigger.headers.X-Event }}-literal"`: true,
		`"literal"`:                  true,
		`"$${{ vars.A }}"`:           true,
		`"${{ vars.A }}"`:            false,
		`"${{ inputs.x }}"`:          false,
		`"${{ execution.id }}"`:      false,
		`"${{ secret('K') }}"`:       false,
		`"${{ tasks.a.outputs.o }}"`: false,
		`"${{ trigger.body.x }}-${{ execution.flow_id }}"`: false,
	}
	for value, ok := range cases {
		res := ValidateNamespace(map[string][]byte{"f.flow.yaml": []byte(head + value + tail)})
		if len(res.Flows) != 1 {
			t.Fatalf("%s: %d flows", value, len(res.Flows))
		}
		pf := res.Flows[0]
		var got []Issue
		for _, is := range pf.Issues {
			if is.Code == CodeTriggerInputRef {
				got = append(got, is)
			}
		}
		if ok {
			if !pf.Valid() {
				t.Errorf("%s: want valid, got %v", value, pf.Issues)
			}
			continue
		}
		if len(got) != 1 {
			t.Errorf("%s: want one %s issue, got %v", value, CodeTriggerInputRef, pf.Issues)
			continue
		}
		is := got[0]
		if is.Path != "triggers[0].inputs.x" || is.Line != 8 || is.Column == 0 || !strings.Contains(is.Message, "trigger.<path>") {
			t.Errorf("%s: issue %+v", value, is)
		}
	}
}
