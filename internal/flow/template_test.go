package flow

import (
	"errors"
	"strings"
	"testing"
)

func TestTemplateRender(t *testing.T) {
	c := &Context{
		Inputs:    map[string]any{"full_refresh": true, "name": "orders", "n": 3.0, "obj": map[string]any{"a": 1}},
		Vars:      map[string]string{"DATASET": "raw"},
		Tasks:     map[string]map[string]any{"extract": {"rows": 1234.0}},
		Trigger:   map[string]any{"body": map[string]any{"x": "y"}, "headers": map[string]any{"X-Event": "push"}},
		Execution: map[string]any{"id": "e1", "namespace": "data", "flow_id": "f", "created_at": "2026-01-01T00:00:00Z"},
		Secret:    func(k string) (string, error) { return "s3cret-" + k, nil },
	}
	cases := map[string]string{
		"--full-refresh=${{ inputs.full_refresh }}":            "--full-refresh=true",
		"${{inputs.name}}-${{ vars.DATASET }}":                 "orders-raw",
		`{"rows": "${{ tasks.extract.outputs.rows }}"}`:        `{"rows": "1234"}`,
		"${{ inputs.obj }}":                                    `{"a":1}`,
		"${{ trigger.body.x }} ${{ trigger.headers.x-event }}": "y push",
		"${{ execution.id }}/${{ execution.namespace }}":       "e1/data",
		"Bearer ${{ secret('HOOK') }}":                         "Bearer s3cret-HOOK",
		`${{ secret("A") }}`:                                   "s3cret-A",
		"literal $${{ inputs.name }}":                          "literal ${{ inputs.name }}",
		"no refs":                                              "no refs",
	}
	for in, want := range cases {
		got, err := RenderString(in, c)
		if err != nil || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, err, want)
		}
	}
}

func TestTemplateErrors(t *testing.T) {
	bad := []string{"${{ inputs.a", "${{ }}", "${{ foo.bar }}", "${{ inputs.a + 1 }}", "${{ tasks.a.b }}", "${{ execution.nope }}", "${{ secret(KEY) }}", "${{ vars.a.b }}"}
	for _, s := range bad {
		if _, err := ParseTemplate(s); err == nil {
			t.Errorf("%q parsed", s)
		}
	}
	_, err := RenderString("${{ tasks.x.outputs.rows }}", &Context{Tasks: map[string]map[string]any{"x": {}}})
	var te *TemplateError
	if !errors.As(err, &te) || !strings.Contains(err.Error(), "no output") {
		t.Fatalf("missing output: %v", err)
	}
	if _, err := RenderString("${{ secret('A') }}", &Context{}); err == nil {
		t.Fatal("secret without resolver rendered")
	}
}

func TestTemplateRefs(t *testing.T) {
	tpl, err := ParseTemplate("a ${{ inputs.x }} b ${{ tasks.t1.outputs.o }} ${{ secret('K') }}")
	if err != nil {
		t.Fatal(err)
	}
	refs := tpl.Refs()
	if len(refs) != 3 || refs[0].Kind != RefInputs || refs[1].Key() != "t1" || refs[2].Kind != RefSecret || refs[2].Key() != "K" {
		t.Fatalf("refs %+v", refs)
	}
	if refs[1].Pos != 20 {
		t.Fatalf("pos %d", refs[1].Pos)
	}
}
