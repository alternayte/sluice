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

// TestTaskFiles checks the files map: secret() is allowed in values, keys must be
// relative paths without templates, and http tasks cannot have files.
func TestTaskFiles(t *testing.T) {
	const head = "id: f\ntasks:\n  - id: a\n    type: command\n    command: [\"cat\", \"conf/app.ini\"]\n    files:\n      "
	cases := map[string]string{
		`conf/app.ini: "token=${{ secret('K') }}"`: "",
		`"../x": "v"`:               CodeInvalidPath,
		`"/etc/x": "v"`:             CodeInvalidPath,
		`"${{ vars.p }}": "v"`:      CodeTemplateNotAllowed,
		`a.txt: "${{ inputs.no }}"`: CodeUnknownInput,
	}
	for entry, code := range cases {
		res := ValidateNamespace(map[string][]byte{"f.flow.yaml": []byte(head + entry + "\n")})
		pf := res.Flows[0]
		if code == "" {
			if !pf.Valid() {
				t.Errorf("%s: want valid, got %v", entry, pf.Issues)
			}
			continue
		}
		found := false
		for _, is := range pf.Issues {
			found = found || is.Code == code
		}
		if !found {
			t.Errorf("%s: want %s, got %v", entry, code, pf.Issues)
		}
	}
	res := ValidateNamespace(map[string][]byte{"f.flow.yaml": []byte("id: f\ntasks:\n  - id: a\n    type: http\n    url: http://x\n    files:\n      a.txt: v\n")})
	if res.Flows[0].Valid() {
		t.Error("http task with files: want invalid")
	}
}
