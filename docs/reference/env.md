# Environment variables

Generated from `internal/app/config.go` by `just gen`. Do not edit.

| Variable | Default | Description |
|---|---|---|
| `SLUICE_DATABASE_URL` | required | Postgres URL. A pooled URL (PgBouncer transaction mode, Neon pooler) is allowed. |
| `SLUICE_LISTEN_ADDR` | `:8080` | HTTP listen address. |
| `SLUICE_PUBLIC_URL` | required for `server` | External base URL for links, cookies and webhooks. An https URL makes the session cookie Secure. |
| `SLUICE_INTERNAL_URL` | `http://127.0.0.1:<port>` | Base URL that runners call. Use the Service URL in Kubernetes. |
| `SLUICE_MASTER_KEYS` | empty | Master keys for builtin secrets as kid:base64key pairs separated by commas. The first key is active. |
| `SLUICE_BOOTSTRAP_ADMIN_EMAIL` | empty | Email of the first admin. Used only when the users table is empty. |
| `SLUICE_BOOTSTRAP_ADMIN_PASSWORD` | empty | Password of the first admin. Used only when the users table is empty. |
| `SLUICE_SESSION_TTL` | `168h` | Sliding session lifetime. |
| `SLUICE_LOG_LEVEL` | `info` | Server log level. |
| `SLUICE_LOG_FORMAT` | `json` | Server log format. |
| `SLUICE_POOLS` | `default` | Comma-separated pools that this instance serves. |
| `SLUICE_EXECUTORS` | `auto` | auto, or a comma-separated list of process, docker and kubernetes. inline is always enabled. |
| `SLUICE_WORKER_SLOTS` | `8` | Slots for process and docker tasks on this instance. |
| `SLUICE_QUEUE_POLL_INTERVAL` | `1s` | Interval between queue claim polls. |
| `SLUICE_HEARTBEAT_TIMEOUT` | `60s` | Time without runner heartbeat after which a task run is checked and can become lost. |
| `SLUICE_SHUTDOWN_GRACE` | `30s` | Maximum time between SIGTERM and process exit. |
| `SLUICE_RETENTION_DAYS` | `90` | Days to keep ended executions with their logs, metrics and artifacts. |
| `SLUICE_STORAGE_TYPE` | `postgres` | Object storage driver. |
| `SLUICE_FS_ROOT` | empty | Root directory of the fs storage driver. |
| `SLUICE_S3_BUCKET` | empty | Bucket of the s3 storage driver. |
| `SLUICE_S3_REGION` | empty | Region of the s3 storage driver. |
| `SLUICE_S3_ENDPOINT` | empty | Endpoint override of the s3 storage driver (Cloudflare R2, MinIO). |
| `SLUICE_S3_FORCE_PATH_STYLE` | `false` | Use path-style addressing in the s3 storage driver. |
| `SLUICE_S3_ACCESS_KEY_ID` | empty | Static access key ID. Empty uses the default AWS credential chain. |
| `SLUICE_S3_SECRET_ACCESS_KEY` | empty | Static secret access key. Empty uses the default AWS credential chain. |
| `SLUICE_S3_PREFIX` | empty | Key prefix in the s3 bucket. |
| `SLUICE_AZBLOB_ACCOUNT_URL` | empty | Account URL of the azblob storage driver. Uses DefaultAzureCredential. |
| `SLUICE_AZBLOB_CONTAINER` | empty | Container of the azblob storage driver. |
| `SLUICE_AZBLOB_CONNECTION_STRING` | empty | Connection string of the azblob storage driver. Used instead of the account URL. |
| `SLUICE_AZBLOB_PREFIX` | empty | Key prefix in the azblob container. |
| `SLUICE_MAX_FILE_BYTES` | `10MiB` | Maximum size of one namespace file. |
| `SLUICE_MAX_BUNDLE_BYTES` | `200MiB` | Maximum total size of one snapshot. |
| `SLUICE_MAX_ARTIFACT_BYTES` | `100MiB` | Maximum size of one artifact. |
| `SLUICE_RUNNER_IMAGE` | `sluice:<version>` | Image that holds the runner binary for injection into docker and kubernetes tasks. |
| `SLUICE_DOCKER_API_URL` | `http://host.docker.internal:<port>` | Base URL that runners in docker containers call. |
| `SLUICE_DOCKER_KEEP_CONTAINERS` | `false` | Keep docker task containers after completion. |
| `SLUICE_K8S_KUBECONFIG` | empty | Path to a kubeconfig for out-of-cluster access. |
| `SLUICE_K8S_NAMESPACE` | `<own namespace>` | Kubernetes namespace for task Jobs. Default is the namespace of the server pod. |
| `SLUICE_K8S_MAX_JOBS` | `50` | Maximum running Jobs per pool. |
| `SLUICE_K8S_JOB_TTL` | `600s` | ttlSecondsAfterFinished of task Jobs. |
| `SLUICE_K8S_PENDING_TIMEOUT` | `10m` | Maximum time a task pod can stay pending. |
| `SLUICE_SECRET_CACHE_TTL` | `60s` | Cache lifetime of external secret values. |
| `SLUICE_VAULT_ADDR` | empty | HashiCorp Vault address. |
| `SLUICE_VAULT_TOKEN` | empty | HashiCorp Vault token. |
| `SLUICE_VAULT_K8S_ROLE` | empty | HashiCorp Vault Kubernetes auth role. Used when no token is set. |
| `SLUICE_AI_MAX_CONTEXT_CHARS` | `120000` | Maximum characters of model context. |
| `SLUICE_SECRET_<KEY>` | empty | Value of secret `<KEY>` for the env secret provider. |

Azure credentials use the standard `AZURE_*` variables, workload identity or managed identity.
