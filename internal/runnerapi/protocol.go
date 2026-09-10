// Package runnerapi holds the runner protocol (Appendix C): the shared message types
// and the server side of /api/runner/v1.
package runnerapi

import (
	"encoding/json"
	"time"
)

// BasePath is the prefix of the runner API.
const BasePath = "/api/runner/v1"

// Environment variables of the runner (§4.3, §6.10).
const (
	EnvAPIURL      = "SLUICE_API_URL"
	EnvRunToken    = "SLUICE_RUN_TOKEN"
	EnvTaskRunID   = "SLUICE_TASK_RUN_ID"
	EnvExecutionID = "SLUICE_EXECUTION_ID"
	EnvTaskID      = "SLUICE_TASK_ID"
	EnvAttempt     = "SLUICE_ATTEMPT"
	EnvNamespace   = "SLUICE_NAMESPACE"
	EnvFlowID      = "SLUICE_FLOW_ID"
	EnvOutputs     = "SLUICE_OUTPUTS"
	EnvWorkdir     = "SLUICE_WORKDIR"
)

// Limits of the protocol (REQ-RUN-002, §6.9).
const (
	MaxLineBytes       = 16 << 10
	BatchInterval      = 500 * time.Millisecond
	BatchBytes         = 256 << 10
	HeartbeatInterval  = 10 * time.Second
	KillAfter          = 10 * time.Second
	RetryWindow        = 5 * time.Minute
	PendingBufferBytes = 64 << 20
	MaxOutputBytes     = 1 << 20
	MaxMetrics         = 10000
	MaxMetricTags      = 8
	MaxTagValueLen     = 128
	TruncatedMarker    = " …[truncated]"
)

// Spec is the response of GET /task-runs/{id}/spec.
type Spec struct {
	TaskRunID   string            `json:"task_run_id"`
	ExecutionID string            `json:"execution_id"`
	Namespace   string            `json:"namespace"`
	FlowID      string            `json:"flow_id"`
	TaskID      string            `json:"task_id"`
	Attempt     int               `json:"attempt"`
	Command     []string          `json:"command"`
	Workdir     string            `json:"workdir"`
	Env         map[string]string `json:"env"`
	// Runtime is the tool that must exist on PATH (uv, bash, bun, node) or "".
	Runtime        string   `json:"runtime,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	MaskValues     []string `json:"mask_values"`
	BundleHash     string   `json:"bundle_hash"`
	Limits         Limits   `json:"limits"`
}

// Limits sent to the runner.
type Limits struct {
	MaxArtifactBytes int64 `json:"max_artifact_bytes"`
	MaxBundleBytes   int64 `json:"max_bundle_bytes"`
}

// LogLine is one log line.
type LogLine struct {
	TS     time.Time `json:"ts"`
	Stream string    `json:"stream"` // stdout, stderr, system
	Text   string    `json:"text"`
}

// LogBatch is the body of POST /task-runs/{id}/logs.
type LogBatch struct {
	Seq   int       `json:"seq"`
	Lines []LogLine `json:"lines"`
}

// Event is one output or metric event.
type Event struct {
	Type  string            `json:"type"` // output, metric
	Key   string            `json:"key,omitempty"`
	Value json.RawMessage   `json:"value,omitempty"`
	Name  string            `json:"name,omitempty"`
	Unit  string            `json:"unit,omitempty"`
	Tags  map[string]string `json:"tags,omitempty"`
	TS    time.Time         `json:"ts,omitempty"`
}

// EventBatch is the body of POST /task-runs/{id}/events.
type EventBatch struct {
	Seq    int     `json:"seq"`
	Events []Event `json:"events"`
}

// HeartbeatResponse is the response of POST /task-runs/{id}/heartbeat.
type HeartbeatResponse struct {
	Cancel bool `json:"cancel"`
}

// Complete is the body of POST /task-runs/{id}/complete.
type Complete struct {
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error"`
	// Reason is set by the runner for runtime_not_found, timeout and cancelled.
	Reason string `json:"reason,omitempty"`
}
