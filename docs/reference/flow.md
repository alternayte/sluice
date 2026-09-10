# Flow reference

Generated from `internal/flow/model.go` by `just gen`. Do not edit.

A flow file matches `*.flow.yaml` or `*.flow.yml`. `namespace.yaml` at the namespace root sets defaults.

## Flow

| Field | Type | Description |
|---|---|---|
| `id` | string | Flow ID. Lower case letters, digits and hyphens. At most 63 characters. Unique in the namespace. Required. |
| `description` | string | Free text description. |
| `labels` | map of string | Labels that every execution of the flow carries. At most 20 entries. |
| `inputs` | list of Input | Inputs of the flow. They are validated at trigger time. |
| `variables` | map of string | Static values for vars. They have the highest precedence. |
| `env` | map of string | Environment templates for all tasks. Task env overrides by key. |
| `triggers` | list of Trigger | Schedule, webhook and flow triggers. Manual triggering needs no declaration. |
| `concurrency` | Concurrency | Limit of concurrent executions. Absent means unlimited. |
| `max_parallel` | int | Maximum tasks that run at the same time. 0 means unlimited. |
| `timeout` | duration | Execution wall time. Default is no limit. |
| `retry` | Retry | Default retry policy of the tasks. |
| `executor` | Executor | Default executor of the tasks. |
| `tasks` | list of Task | Tasks of the flow. From 1 to 200. Required. |
| `outputs` | map of string | Output templates. They are resolved when the execution succeeds. |

## Input

| Field | Type | Description |
|---|---|---|
| `id` | string | Input ID. Required. |
| `type` | string | Input type. Required. |
| `required` | boolean | A trigger must give a value when there is no default. |
| `default` | any | Default value. It must match the type. |
| `values` | list of any | Allowed values of a select input. |
| `description` | string | Help text for the run form. |

## Trigger

| Field | Type | Description |
|---|---|---|
| `id` | string | Trigger ID. Unique in the flow. Required. |
| `type` | string | Trigger type. Required. |
| `cron` | string | schedule: five cron fields, or @hourly, @daily, @weekly or @monthly. |
| `timezone` | string | schedule: IANA time zone. Default UTC. |
| `catch_up` | string | schedule: last fires only the latest missed time, none fires no missed time. Default last. |
| `flow` | string | flow: upstream flow as <namespace>/<flow_id>. |
| `states` | list of string | flow: upstream end states that fire the trigger (SUCCESS, FAILED, TIMED_OUT, CANCELLED). |
| `inputs` | map of string | Input templates. Webhooks use trigger.body and trigger.headers. Flow triggers use trigger.outputs. |

## Concurrency

| Field | Type | Description |
|---|---|---|
| `limit` | int | Maximum running executions. Required. |
| `behavior` | string | queue holds new executions in QUEUED. skip sets them to SKIPPED. Default queue. |

## Retry

| Field | Type | Description |
|---|---|---|
| `max_attempts` | int | Attempts including the first. From 1 to 20. Default 1. |
| `backoff` | string | Delay growth between attempts. Default fixed. |
| `initial` | duration | First delay. Default 10s. |
| `max` | duration | Maximum delay. Default 10m. |

## Executor

| Field | Type | Description |
|---|---|---|
| `type` | string | Executor type. Resolution order: task, flow, namespace.yaml, instance default. |
| `pool` | string | Pool of the instances that run the task. Default default. |
| `image` | string | docker and kubernetes: container image. Required for these types. |
| `inject_runner` | boolean | docker and kubernetes: copy the runner into the container. Default true. False needs sluice on the image PATH. |
| `pull` | string | docker: image pull policy. Default if_not_present. |
| `network` | string | docker: network name. |
| `resources` | Resources | docker and kubernetes: requests and limits. Docker uses limits. |
| `kubernetes` | Kubernetes | kubernetes: pod settings. |

## Resources

| Field | Type | Description |
|---|---|---|
| `requests` | ResourceList | Resource requests. |
| `limits` | ResourceList | Resource limits. |

## ResourceList

| Field | Type | Description |
|---|---|---|
| `cpu` | string | CPU quantity, for example 500m or 2. |
| `memory` | string | Memory quantity, for example 512Mi. |

## Kubernetes

| Field | Type | Description |
|---|---|---|
| `service_account` | string | Service account of the pod. |
| `node_selector` | map of string | Node selector of the pod. |
| `tolerations` | list of Toleration | Tolerations of the pod. |
| `image_pull_secrets` | list of string | Names of image pull secrets. |
| `labels` | map of string | Extra pod labels. |
| `annotations` | map of string | Extra pod annotations. |

## Toleration

| Field | Type | Description |
|---|---|---|
| `key` | string | Taint key. |
| `operator` | string | Exists or Equal. |
| `value` | string | Taint value. |
| `effect` | string | Taint effect. |
| `toleration_seconds` | int | Seconds to tolerate a NoExecute taint. |

## Task

| Field | Type | Description |
|---|---|---|
| `id` | string | Task ID. Unique in the flow. Required. |
| `type` | string | Task type. Required. |
| `depends_on` | list of string | IDs of tasks that must end first. |
| `run_if` | string | success: all dependencies succeeded. failure: at least one dependency failed or timed out. always: all dependencies ended. Default success. |
| `timeout` | duration | Task timeout. Default 24h. |
| `retry` | Retry | Retry policy. Overrides the flow retry. |
| `env` | map of string | Environment templates. Override flow env by key. |
| `executor` | Executor | Executor. Not allowed on http and subflow tasks. |
| `file` | string | script: file path relative to the namespace root. Required for script. |
| `runtime` | string | script: runtime. Default from the extension (.py, .sh, .ts, .js). |
| `args` | list of string | script: argument templates. |
| `command` | list of string | command: argv templates. No shell. Required for command. |
| `workdir` | string | command: working directory relative to the namespace root. Default root. |
| `method` | string | http: request method. Default GET. |
| `url` | string | http: URL template. Required for http. |
| `headers` | map of string | http: header templates. |
| `body` | string | http: body template. |
| `expect_status` | list of int | http: accepted status codes. Default 200 to 299. |
| `flow` | string | subflow: child flow as <namespace>/<flow_id>. Required for subflow. |
| `inputs` | map of string | subflow: input templates of the child flow. |
| `wait` | boolean | subflow: wait for the child to end. Default true. |

## NamespaceFile (namespace.yaml)

| Field | Type | Description |
|---|---|---|
| `description` | string | Namespace description. |
| `defaults` | Defaults | Defaults for all flows of the namespace. |

## Defaults

| Field | Type | Description |
|---|---|---|
| `executor` | Executor | Default executor. |
| `env` | map of string | Default environment. Flow and task env override by key. |
| `retry` | Retry | Default retry policy. |
| `timeout` | duration | Default task timeout. |
