// Package flow holds the flow model, parsing, validation, JSON Schema and templates (§6).
package flow

import "github.com/invopop/jsonschema"

// Flow is one flow file (§6.3). The struct tags are the source of
// schemas/flow.schema.json and docs/reference/flow.md.
type Flow struct {
	ID          string            `yaml:"id" json:"id" jsonschema:"required,pattern=^[a-z0-9][a-z0-9-]*$,maxLength=63" jsonschema_description:"Flow ID. Lower case letters, digits and hyphens. At most 63 characters. Unique in the namespace."`
	Description string            `yaml:"description,omitempty" json:"description,omitempty" jsonschema:"maxLength=2000" jsonschema_description:"Free text description."`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty" jsonschema:"maxProperties=20" jsonschema_description:"Labels that every execution of the flow carries. At most 20 entries."`
	Inputs      []Input           `yaml:"inputs,omitempty" json:"inputs,omitempty" jsonschema_description:"Inputs of the flow. They are validated at trigger time."`
	Variables   map[string]string `yaml:"variables,omitempty" json:"variables,omitempty" jsonschema_description:"Static values for vars. They have the highest precedence."`
	Env         map[string]string `yaml:"env,omitempty" json:"env,omitempty" jsonschema_description:"Environment templates for all tasks. Task env overrides by key."`
	Triggers    []Trigger         `yaml:"triggers,omitempty" json:"triggers,omitempty" jsonschema_description:"Schedule, webhook and flow triggers. Manual triggering needs no declaration."`
	Concurrency *Concurrency      `yaml:"concurrency,omitempty" json:"concurrency,omitempty" jsonschema_description:"Limit of concurrent executions. Absent means unlimited."`
	MaxParallel int               `yaml:"max_parallel,omitempty" json:"max_parallel,omitempty" jsonschema:"minimum=0,default=0" jsonschema_description:"Maximum tasks that run at the same time. 0 means unlimited."`
	Timeout     Duration          `yaml:"timeout,omitempty" json:"timeout,omitempty" jsonschema_description:"Execution wall time. Default is no limit."`
	Retry       *Retry            `yaml:"retry,omitempty" json:"retry,omitempty" jsonschema_description:"Default retry policy of the tasks."`
	Executor    *Executor         `yaml:"executor,omitempty" json:"executor,omitempty" jsonschema_description:"Default executor of the tasks."`
	Tasks       []Task            `yaml:"tasks" json:"tasks" jsonschema:"required,minItems=1,maxItems=200" jsonschema_description:"Tasks of the flow. From 1 to 200."`
	Outputs     map[string]string `yaml:"outputs,omitempty" json:"outputs,omitempty" jsonschema_description:"Output templates. They are resolved when the execution succeeds."`
}

// Input is one flow input.
type Input struct {
	ID          string `yaml:"id" json:"id" jsonschema:"required,pattern=^[a-z][a-z0-9_]*$,maxLength=63" jsonschema_description:"Input ID."`
	Type        string `yaml:"type" json:"type" jsonschema:"required,enum=string,enum=int,enum=number,enum=boolean,enum=select,enum=json" jsonschema_description:"Input type."`
	Required    bool   `yaml:"required,omitempty" json:"required,omitempty" jsonschema_description:"A trigger must give a value when there is no default."`
	Default     any    `yaml:"default,omitempty" json:"default,omitempty" jsonschema_description:"Default value. It must match the type."`
	Values      []any  `yaml:"values,omitempty" json:"values,omitempty" jsonschema_description:"Allowed values of a select input."`
	Description string `yaml:"description,omitempty" json:"description,omitempty" jsonschema_description:"Help text for the run form."`
}

// Trigger is one declared trigger (§6.5).
type Trigger struct {
	ID       string            `yaml:"id" json:"id" jsonschema:"required,pattern=^[a-z][a-z0-9_-]*$,maxLength=63" jsonschema_description:"Trigger ID. Unique in the flow."`
	Type     string            `yaml:"type" json:"type" jsonschema:"required,enum=schedule,enum=webhook,enum=flow" jsonschema_description:"Trigger type."`
	Cron     string            `yaml:"cron,omitempty" json:"cron,omitempty" jsonschema_description:"schedule: five cron fields, or @hourly, @daily, @weekly or @monthly."`
	Timezone string            `yaml:"timezone,omitempty" json:"timezone,omitempty" jsonschema_description:"schedule: IANA time zone. Default UTC."`
	CatchUp  string            `yaml:"catch_up,omitempty" json:"catch_up,omitempty" jsonschema:"enum=none,enum=last" jsonschema_description:"schedule: last fires only the latest missed time, none fires no missed time. Default last."`
	Flow     string            `yaml:"flow,omitempty" json:"flow,omitempty" jsonschema_description:"flow: upstream flow as <namespace>/<flow_id>."`
	States   []string          `yaml:"states,omitempty" json:"states,omitempty" jsonschema_description:"flow: upstream end states that fire the trigger (SUCCESS, FAILED, TIMED_OUT, CANCELLED)."`
	Inputs   map[string]string `yaml:"inputs,omitempty" json:"inputs,omitempty" jsonschema_description:"Input templates. Webhooks use trigger.body and trigger.headers. Flow triggers use trigger.outputs."`
}

// Concurrency limits concurrent executions of a flow.
type Concurrency struct {
	Limit    int    `yaml:"limit" json:"limit" jsonschema:"required,minimum=1" jsonschema_description:"Maximum running executions."`
	Behavior string `yaml:"behavior,omitempty" json:"behavior,omitempty" jsonschema:"enum=queue,enum=skip" jsonschema_description:"queue holds new executions in QUEUED. skip sets them to SKIPPED. Default queue."`
}

// Retry is a retry policy.
type Retry struct {
	MaxAttempts int      `yaml:"max_attempts,omitempty" json:"max_attempts,omitempty" jsonschema:"minimum=1,maximum=20,default=1" jsonschema_description:"Attempts including the first. From 1 to 20. Default 1."`
	Backoff     string   `yaml:"backoff,omitempty" json:"backoff,omitempty" jsonschema:"enum=fixed,enum=exponential" jsonschema_description:"Delay growth between attempts. Default fixed."`
	Initial     Duration `yaml:"initial,omitempty" json:"initial,omitempty" jsonschema_description:"First delay. Default 10s."`
	Max         Duration `yaml:"max,omitempty" json:"max,omitempty" jsonschema_description:"Maximum delay. Default 10m."`
}

// Executor selects where a task runs (§6.4).
type Executor struct {
	Type         string      `yaml:"type,omitempty" json:"type,omitempty" jsonschema:"enum=process,enum=docker,enum=kubernetes" jsonschema_description:"Executor type. Resolution order: task, flow, namespace.yaml, instance default."`
	Pool         string      `yaml:"pool,omitempty" json:"pool,omitempty" jsonschema:"pattern=^[a-z0-9-]+$,maxLength=63" jsonschema_description:"Pool of the instances that run the task. Default default."`
	Image        string      `yaml:"image,omitempty" json:"image,omitempty" jsonschema_description:"docker and kubernetes: container image. Required for these types."`
	InjectRunner *bool       `yaml:"inject_runner,omitempty" json:"inject_runner,omitempty" jsonschema_description:"docker and kubernetes: copy the runner into the container. Default true. False needs sluice on the image PATH."`
	Pull         string      `yaml:"pull,omitempty" json:"pull,omitempty" jsonschema:"enum=always,enum=if_not_present,enum=never" jsonschema_description:"docker: image pull policy. Default if_not_present."`
	Network      string      `yaml:"network,omitempty" json:"network,omitempty" jsonschema_description:"docker: network name."`
	Resources    *Resources  `yaml:"resources,omitempty" json:"resources,omitempty" jsonschema_description:"docker and kubernetes: requests and limits. Docker uses limits."`
	Kubernetes   *Kubernetes `yaml:"kubernetes,omitempty" json:"kubernetes,omitempty" jsonschema_description:"kubernetes: pod settings."`
}

// Resources are requests and limits.
type Resources struct {
	Requests *ResourceList `yaml:"requests,omitempty" json:"requests,omitempty" jsonschema_description:"Resource requests."`
	Limits   *ResourceList `yaml:"limits,omitempty" json:"limits,omitempty" jsonschema_description:"Resource limits."`
}

// ResourceList holds CPU and memory quantities.
type ResourceList struct {
	CPU    string `yaml:"cpu,omitempty" json:"cpu,omitempty" jsonschema:"pattern=^[0-9]+(\\.[0-9]+)?m?$" jsonschema_description:"CPU quantity, for example 500m or 2."`
	Memory string `yaml:"memory,omitempty" json:"memory,omitempty" jsonschema:"pattern=^[0-9]+(Ki|Mi|Gi|Ti|K|M|G|T)?$" jsonschema_description:"Memory quantity, for example 512Mi."`
}

// Kubernetes holds pod settings of the kubernetes executor.
type Kubernetes struct {
	ServiceAccount   string            `yaml:"service_account,omitempty" json:"service_account,omitempty" jsonschema_description:"Service account of the pod."`
	NodeSelector     map[string]string `yaml:"node_selector,omitempty" json:"node_selector,omitempty" jsonschema_description:"Node selector of the pod."`
	Tolerations      []Toleration      `yaml:"tolerations,omitempty" json:"tolerations,omitempty" jsonschema_description:"Tolerations of the pod."`
	ImagePullSecrets []string          `yaml:"image_pull_secrets,omitempty" json:"image_pull_secrets,omitempty" jsonschema_description:"Names of image pull secrets."`
	Labels           map[string]string `yaml:"labels,omitempty" json:"labels,omitempty" jsonschema_description:"Extra pod labels."`
	Annotations      map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty" jsonschema_description:"Extra pod annotations."`
}

// Toleration is a Kubernetes toleration.
type Toleration struct {
	Key               string `yaml:"key,omitempty" json:"key,omitempty" jsonschema_description:"Taint key."`
	Operator          string `yaml:"operator,omitempty" json:"operator,omitempty" jsonschema:"enum=Exists,enum=Equal" jsonschema_description:"Exists or Equal."`
	Value             string `yaml:"value,omitempty" json:"value,omitempty" jsonschema_description:"Taint value."`
	Effect            string `yaml:"effect,omitempty" json:"effect,omitempty" jsonschema:"enum=NoSchedule,enum=PreferNoSchedule,enum=NoExecute" jsonschema_description:"Taint effect."`
	TolerationSeconds *int64 `yaml:"toleration_seconds,omitempty" json:"toleration_seconds,omitempty" jsonschema_description:"Seconds to tolerate a NoExecute taint."`
}

// Task is one task (§6.4). Type-specific fields are checked by semantic validation.
type Task struct {
	ID        string            `yaml:"id" json:"id" jsonschema:"required,pattern=^[a-z][a-z0-9_]*$,maxLength=63" jsonschema_description:"Task ID. Unique in the flow."`
	Type      string            `yaml:"type" json:"type" jsonschema:"required,enum=script,enum=command,enum=http,enum=subflow" jsonschema_description:"Task type."`
	DependsOn []string          `yaml:"depends_on,omitempty" json:"depends_on,omitempty" jsonschema_description:"IDs of tasks that must end first."`
	RunIf     string            `yaml:"run_if,omitempty" json:"run_if,omitempty" jsonschema:"enum=success,enum=failure,enum=always" jsonschema_description:"success: all dependencies succeeded. failure: at least one dependency failed or timed out. always: all dependencies ended. Default success."`
	Timeout   Duration          `yaml:"timeout,omitempty" json:"timeout,omitempty" jsonschema_description:"Task timeout. Default 24h."`
	Retry     *Retry            `yaml:"retry,omitempty" json:"retry,omitempty" jsonschema_description:"Retry policy. Overrides the flow retry."`
	Env       map[string]string `yaml:"env,omitempty" json:"env,omitempty" jsonschema_description:"Environment templates. Override flow env by key."`
	Executor  *Executor         `yaml:"executor,omitempty" json:"executor,omitempty" jsonschema_description:"Executor. Not allowed on http and subflow tasks."`
	Files     map[string]string `yaml:"files,omitempty" json:"files,omitempty" jsonschema:"maxProperties=100" jsonschema_description:"script and command: file templates. The key is a path relative to the namespace root. Sluice writes the rendered value to that path in the workdir before the task starts, and replaces a namespace file at the same path. secret() is allowed."`

	// script
	File    string   `yaml:"file,omitempty" json:"file,omitempty" jsonschema_description:"script: file path relative to the namespace root. Required for script."`
	Runtime string   `yaml:"runtime,omitempty" json:"runtime,omitempty" jsonschema:"enum=python,enum=bash,enum=bun,enum=node" jsonschema_description:"script: runtime. Default from the extension (.py, .sh, .ts, .js)."`
	Args    []string `yaml:"args,omitempty" json:"args,omitempty" jsonschema_description:"script: argument templates."`

	// command
	Command []string `yaml:"command,omitempty" json:"command,omitempty" jsonschema_description:"command: argv templates. No shell. Required for command."`
	Workdir string   `yaml:"workdir,omitempty" json:"workdir,omitempty" jsonschema_description:"command: working directory relative to the namespace root. Default root."`

	// http
	Method       string            `yaml:"method,omitempty" json:"method,omitempty" jsonschema:"enum=GET,enum=POST,enum=PUT,enum=PATCH,enum=DELETE,enum=HEAD" jsonschema_description:"http: request method. Default GET."`
	URL          string            `yaml:"url,omitempty" json:"url,omitempty" jsonschema_description:"http: URL template. Required for http."`
	Headers      map[string]string `yaml:"headers,omitempty" json:"headers,omitempty" jsonschema_description:"http: header templates."`
	Body         string            `yaml:"body,omitempty" json:"body,omitempty" jsonschema_description:"http: body template."`
	ExpectStatus []int             `yaml:"expect_status,omitempty" json:"expect_status,omitempty" jsonschema_description:"http: accepted status codes. Default 200 to 299."`

	// subflow
	Flow   string            `yaml:"flow,omitempty" json:"flow,omitempty" jsonschema_description:"subflow: child flow as <namespace>/<flow_id>. Required for subflow."`
	Inputs map[string]string `yaml:"inputs,omitempty" json:"inputs,omitempty" jsonschema_description:"subflow: input templates of the child flow."`
	Wait   *bool             `yaml:"wait,omitempty" json:"wait,omitempty" jsonschema_description:"subflow: wait for the child to end. Default true."`
}

// NamespaceFile is namespace.yaml (§6.8).
type NamespaceFile struct {
	Description string    `yaml:"description,omitempty" json:"description,omitempty" jsonschema:"maxLength=2000" jsonschema_description:"Namespace description."`
	Defaults    *Defaults `yaml:"defaults,omitempty" json:"defaults,omitempty" jsonschema_description:"Defaults for all flows of the namespace."`
}

// Defaults are namespace defaults.
type Defaults struct {
	Executor *Executor         `yaml:"executor,omitempty" json:"executor,omitempty" jsonschema_description:"Default executor."`
	Env      map[string]string `yaml:"env,omitempty" json:"env,omitempty" jsonschema_description:"Default environment. Flow and task env override by key."`
	Retry    *Retry            `yaml:"retry,omitempty" json:"retry,omitempty" jsonschema_description:"Default retry policy."`
	Timeout  Duration          `yaml:"timeout,omitempty" json:"timeout,omitempty" jsonschema_description:"Default task timeout."`
}

// Duration is a Go duration string such as 30s, 10m or 2h.
type Duration string

// JSONSchema describes Duration as a pattern string.
func (Duration) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Pattern: `^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`, Description: "Duration such as 30s, 10m or 2h."}
}
