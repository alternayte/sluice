package flow

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"sort"
	"strings"
)

// NamespaceFileName is the optional namespace defaults file at the root (§6.8).
const NamespaceFileName = "namespace.yaml"

// IsFlowFile reports whether p is a flow file (§6.1).
func IsFlowFile(p string) bool {
	base := path.Base(p)
	return strings.HasSuffix(base, ".flow.yaml") || strings.HasSuffix(base, ".flow.yml")
}

// ParsedFlow is one flow file after parsing and validation.
type ParsedFlow struct {
	Path       string
	Source     string
	SourceHash string
	Flow       *Flow // nil when the file cannot be decoded
	Positions  Positions
	Issues     []Issue
}

// Valid reports whether the flow has no issues.
func (p *ParsedFlow) Valid() bool { return p.Flow != nil && len(p.Issues) == 0 }

// Key is the flow ID, or a key derived from the file name when the ID is unknown.
func (p *ParsedFlow) Key() string {
	if p.Flow != nil && p.Flow.ID != "" {
		return p.Flow.ID
	}
	base := path.Base(p.Path)
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".yml"), ".yaml")
	base = strings.TrimSuffix(base, ".flow")
	return "invalid:" + p.Path + ":" + base
}

// NamespaceResult is the validation of one namespace tree.
type NamespaceResult struct {
	Namespace       *NamespaceFile
	NamespaceIssues []Issue
	Flows           []*ParsedFlow
}

// SourceHash returns the hash of flow source text.
func SourceHash(src []byte) string {
	h := sha256.Sum256(src)
	return hex.EncodeToString(h[:])
}

// ValidateNamespace parses namespace.yaml and all flow files, runs semantic validation
// and marks duplicate flow IDs (REQ-FLOW-001).
func ValidateNamespace(files map[string][]byte) *NamespaceResult {
	res := &NamespaceResult{}
	exists := map[string]bool{}
	for p := range files {
		exists[p] = true
	}
	var defaults *Defaults
	if src, ok := files[NamespaceFileName]; ok {
		nf, issues := ParseNamespaceFile(src)
		res.Namespace = nf
		res.NamespaceIssues = issues
		if nf != nil && len(issues) == 0 {
			defaults = nf.Defaults
		}
	}
	var paths []string
	for p := range files {
		if IsFlowFile(p) {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	byID := map[string][]*ParsedFlow{}
	for _, p := range paths {
		pf := ParseFlow(p, files[p])
		if pf.Flow != nil && len(pf.Issues) == 0 {
			pf.Issues = Semantic(pf.Flow, pf.Positions, exists, defaults)
		}
		if pf.Flow != nil && pf.Flow.ID != "" {
			byID[pf.Flow.ID] = append(byID[pf.Flow.ID], pf)
		}
		res.Flows = append(res.Flows, pf)
	}
	for id, list := range byID {
		if len(list) < 2 {
			continue
		}
		var others []string
		for _, pf := range list {
			others = append(others, pf.Path)
		}
		for _, pf := range list {
			pf.Issues = append(pf.Issues, pf.Positions.issue(CodeDuplicateFlowID, "id", "flow ID %q is used by %s", id, strings.Join(others, ", ")))
			SortIssues(pf.Issues)
		}
	}
	return res
}

// ValidateResult is the output of `sluice validate --json` (REQ-FLOW-007).
type ValidateResult struct {
	Valid bool         `json:"valid" jsonschema:"required" jsonschema_description:"True when every file is valid."`
	Files []FileResult `json:"files" jsonschema:"required" jsonschema_description:"Validated files in path order."`
}

// FileResult is the validation of one file.
type FileResult struct {
	Path   string  `json:"path" jsonschema:"required" jsonschema_description:"File path relative to the namespace root."`
	Kind   string  `json:"kind" jsonschema:"required,enum=flow,enum=namespace" jsonschema_description:"flow for flow files, namespace for namespace.yaml."`
	FlowID string  `json:"flow_id,omitempty" jsonschema_description:"Flow ID when the file declares one."`
	Valid  bool    `json:"valid" jsonschema:"required" jsonschema_description:"True when the file has no errors."`
	Errors []Issue `json:"errors" jsonschema:"required" jsonschema_description:"Errors of the file."`
}

// Result converts the namespace result into the validate output.
func (r *NamespaceResult) Result() ValidateResult {
	out := ValidateResult{Valid: true, Files: []FileResult{}}
	if r.Namespace != nil || len(r.NamespaceIssues) > 0 {
		fr := FileResult{Path: NamespaceFileName, Kind: "namespace", Valid: len(r.NamespaceIssues) == 0, Errors: nonNil(r.NamespaceIssues)}
		out.Files = append(out.Files, fr)
		out.Valid = out.Valid && fr.Valid
	}
	for _, pf := range r.Flows {
		fr := FileResult{Path: pf.Path, Kind: "flow", Valid: pf.Valid(), Errors: nonNil(pf.Issues)}
		if pf.Flow != nil {
			fr.FlowID = pf.Flow.ID
		}
		out.Files = append(out.Files, fr)
		out.Valid = out.Valid && fr.Valid
	}
	sort.SliceStable(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	return out
}

func nonNil(is []Issue) []Issue {
	if is == nil {
		return []Issue{}
	}
	return is
}
