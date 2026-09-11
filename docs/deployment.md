# Deployment

This document holds the reference of the deployment options, the storage drivers, upgrades and the CLI. `README.md` links here. The design is in SDD §2, §7.3 and §7.15 and in the decisions DI-2, DI-5, DI-17, DI-34 and DI-35 of [build/decisions.md](build/decisions.md).

Sluice is one Go binary. The same binary is the server, the runner and the CLI (C-01). Postgres holds all state (C-02). Configuration comes only from environment variables (C-04). This document names variables but does not repeat their defaults. Every variable is in [reference/env.md](reference/env.md).

| Option | Use it for | Source |
|---|---|---|
| [Single container](#single-container) | One host, Azure Container Apps, any container platform. | `deploy/docker/Dockerfile` |
| [Docker compose](#docker-compose) | Local trials. | `deploy/compose/compose.yml` |
| [Coolify](#coolify) | A Coolify server. | The Dockerfile or the compose file |
| [Kubernetes with Helm](#kubernetes-with-helm) | Production. Kubernetes is the primary target (C-08). | `deploy/helm/sluice` |

## Images

Two images build from `deploy/docker/Dockerfile` (REQ-DEP-001). Both hold the binary at `/usr/local/bin/sluice`. Both run as the non-root user 65532. The entrypoint is `sluice` and the command is `server`.

| Target | Contents | Use it for |
|---|---|---|
| `sluice` | Distroless static image. The binary only. No shell. | The server when all tasks run on docker or kubernetes. The source of the runner binary. |
| `sluice-uv` | Debian slim with `bash`, `git`, `uv`, a uv-managed Python 3.12 and `bun`. | The server when tasks run on the process executor. A task image for Python, bash and bun. |

Build both images with the justfile:

```sh
just build-images
```

This recipe tags the images `sluice:dev` and `sluice-uv:dev`. To build one image with your own tag, run `docker build`:

```sh
docker build -f deploy/docker/Dockerfile --target sluice-uv \
  --build-arg VERSION=1.0.0 --build-arg COMMIT=$(git rev-parse --short HEAD) \
  -t registry.example.com/sluice-uv:1.0.0 .
```

Push the images to a registry that your hosts can pull from. The test `SCN-DEP-001` builds both images, checks the user 65532 and runs `sluice version` in both. The contents of `sluice-uv` are in [executors.md](executors.md#the-sluice-images).

## Minimum configuration

| Variable | Why |
|---|---|
| `SLUICE_DATABASE_URL` | Required. The Postgres URL. |
| `SLUICE_PUBLIC_URL` | Required for `sluice server`. The external base URL. An `https` URL makes the session cookie Secure. |
| `SLUICE_BOOTSTRAP_ADMIN_EMAIL`, `SLUICE_BOOTSTRAP_ADMIN_PASSWORD` | The first admin. Sluice reads them only when the `users` table is empty. Later starts do not change users (REQ-AUTH-002). |
| `SLUICE_MASTER_KEYS` | Master keys for builtin secrets. Without them, builtin secret writes return `409 builtin_provider_disabled`. |

Invalid configuration stops startup with exit code 2 and lists all errors (REQ-CORE-002).

A master key entry is `kid:base64key`. The key must decode to 32 bytes. The first entry is the active key. Create a key with this command:

```sh
echo "k1:$(openssl rand -base64 32)"
```

Keep the master keys safe. Without the key of a stored key ID, `/readyz` fails with `master_key_missing`. For key rotation, see [secrets-and-variables.md](secrets-and-variables.md) and [the `secrets rekey` command](#secrets-rekey).

## Single container

Sluice runs as one container with no volumes (C-07, REQ-DEP-003). It needs only environment variables and a Postgres database. The default storage driver `postgres` keeps file contents, logs and artifacts in the database, so the container needs no disk.

This mode works on Azure Container Apps. Azure Container Apps is a deployment target only. Sluice has no Azure Container Apps executor (D-12).

1. Create a Postgres database. See [Postgres requirements](#postgres-requirements).
2. Start the `sluice-uv` image:

```sh
docker run -d --name sluice -p 8080:8080 \
  -e SLUICE_DATABASE_URL='postgres://sluice:secret@db.example.com:5432/sluice?sslmode=require' \
  -e SLUICE_PUBLIC_URL=https://sluice.example.com \
  -e SLUICE_EXECUTORS=process \
  -e SLUICE_BOOTSTRAP_ADMIN_EMAIL=admin@example.com \
  -e SLUICE_BOOTSTRAP_ADMIN_PASSWORD='change-me-now' \
  -e SLUICE_MASTER_KEYS="k1:$(openssl rand -base64 32)" \
  sluice-uv:dev
```

3. Open `SLUICE_PUBLIC_URL` and sign in as the bootstrap admin.
4. Keep the value of `SLUICE_MASTER_KEYS` for the next start. The command above creates a new key each time.

Tasks run on the process executor inside the container. They use the `bash`, `uv`, Python and `bun` of `sluice-uv`. The test `SCN-DEP-003` runs `sluice-uv` with only environment variables and no volumes. A uv flow reaches `SUCCESS`. After a container restart the history and the logs are intact.

## Docker compose

`deploy/compose/compose.yml` starts Postgres and Sluice for local use (REQ-DEP-005).

| Service | Image | Notes |
|---|---|---|
| `postgres` | `postgres:17-alpine` | User, password and database `sluice`. The password comes from `POSTGRES_PASSWORD`, default `sluice`. |
| `sluice` | `sluice-uv:dev`, built from the Dockerfile target `sluice-uv` | Starts after Postgres is healthy. `SLUICE_EXECUTORS` is `process`. |

The compose file reads these variables from the shell or from a `.env` file next to it:

| Variable | Default | Notes |
|---|---|---|
| `SLUICE_BOOTSTRAP_ADMIN_PASSWORD` | none | Required. Compose stops when it is not set. |
| `SLUICE_MASTER_KEYS` | none | Required. Compose stops when it is not set. |
| `SLUICE_BOOTSTRAP_ADMIN_EMAIL` | `admin@local.test` | |
| `SLUICE_PUBLIC_URL` | `http://localhost:8080` | |
| `SLUICE_PORT` | `8080` | The host port. |
| `POSTGRES_PASSWORD` | `sluice` | |

1. Set the two required variables:

```sh
export SLUICE_BOOTSTRAP_ADMIN_PASSWORD='change-me-now'
export SLUICE_MASTER_KEYS="k1:$(openssl rand -base64 32)"
```

2. Start the stack from the repository root:

```sh
docker compose -f deploy/compose/compose.yml up -d --build
```

3. Open `http://localhost:8080`.

The test `SCN-DEP-005` proves that `/readyz` answers 200 within 60 s after `docker compose up`.

The compose file declares no named volume for Postgres. Add one before you keep data that you need. `deploy/compose/dev.yml` is a different file. It holds only a Postgres with a volume for local development with `just up`.

## Coolify

Coolify deploys `deploy/docker/Dockerfile` or `deploy/compose/compose.yml`. The environment variables come from the Coolify UI.

- With the Dockerfile, select the build target `sluice-uv` for the process executor. Give Sluice a Postgres database and set the [minimum configuration](#minimum-configuration).
- With the compose file, set `SLUICE_BOOTSTRAP_ADMIN_PASSWORD` and `SLUICE_MASTER_KEYS` in the Coolify UI. Set `SLUICE_PUBLIC_URL` to the public URL that Coolify gives the service.

## Kubernetes with Helm

The chart is in `deploy/helm/sluice` (REQ-DEP-002).

| Object | Notes |
|---|---|
| Deployment | `replicas` pods of the server image with the command `server`. |
| Service | Port `service.port`, default 8080. |
| ServiceAccount | Name from `serviceAccount.name`, or the release full name. |
| Role and RoleBinding | The permissions below. |
| Ingress | Only when `ingress.enabled` is `true`. |

The chart creates no PersistentVolumeClaim. All state is in Postgres (C-03).

### Install

1. Push the images to your registry. See [Images](#images).
2. Create the Secret with the database URL:

```sh
kubectl -n sluice create secret generic sluice-db \
  --from-literal=url='postgres://sluice:secret@postgres.sluice.svc:5432/sluice?sslmode=disable'
```

3. Create the Secret with the master keys:

```sh
kubectl -n sluice create secret generic sluice-master-keys \
  --from-literal=keys="k1:$(openssl rand -base64 32)"
```

4. Create the Secret with the first admin:

```sh
kubectl -n sluice create secret generic sluice-admin \
  --from-literal=email=admin@example.com --from-literal=password='change-me-now'
```

5. Install the chart:

```sh
helm install sluice deploy/helm/sluice -n sluice \
  --set image.repository=registry.example.com/sluice \
  --set image.tag=1.0.0 \
  --set publicURL=https://sluice.example.com \
  --set masterKeys.existingSecret=sluice-master-keys \
  --set bootstrapAdmin.existingSecret=sluice-admin
```

6. Wait until the pods are ready:

```sh
kubectl -n sluice rollout status deployment/sluice
```

The test `SCN-DEP-002` installs the chart into kind with 2 replicas. The namespace has no PVC, the pods have a read-only root file system, `/readyz` is 200 and `helm lint` passes.

### Values

| Value | Default | Sets |
|---|---|---|
| `replicas` | `2` | Number of server pods. |
| `image.repository` | `sluice` | Server image. |
| `image.tag` | `""` | Empty uses the chart `appVersion`. |
| `image.pullPolicy` | `IfNotPresent` | Pull policy of the server image. |
| `runnerImage` | `""` | `SLUICE_RUNNER_IMAGE`. Empty uses the server image. |
| `database.existingSecret` | `sluice-db` | Secret with `SLUICE_DATABASE_URL`. |
| `database.key` | `url` | Key in that Secret. |
| `masterKeys.existingSecret` | `""` | Secret with `SLUICE_MASTER_KEYS`. Empty sets no master keys. |
| `masterKeys.key` | `keys` | Key in that Secret. |
| `bootstrapAdmin.existingSecret` | `""` | Secret with the first admin. Empty sets no bootstrap admin. |
| `bootstrapAdmin.emailKey` | `email` | Key of `SLUICE_BOOTSTRAP_ADMIN_EMAIL`. |
| `bootstrapAdmin.passwordKey` | `password` | Key of `SLUICE_BOOTSTRAP_ADMIN_PASSWORD`. |
| `publicURL` | `http://localhost:8080` | `SLUICE_PUBLIC_URL`. |
| `internalURL` | `""` | `SLUICE_INTERNAL_URL`. Empty uses `http://<fullname>.<namespace>.svc:<service.port>`. |
| `pools` | `[default]` | `SLUICE_POOLS`. |
| `executors` | `auto` | `SLUICE_EXECUTORS`. |
| `storage.type` | `postgres` | `SLUICE_STORAGE_TYPE`. |
| `kubernetes.maxJobs` | `50` | `SLUICE_K8S_MAX_JOBS`. |
| `kubernetes.jobTTL` | `600s` | `SLUICE_K8S_JOB_TTL`. |
| `kubernetes.pendingTimeout` | `10m` | `SLUICE_K8S_PENDING_TIMEOUT`. |
| `kubernetesSecretProvider.enabled` | `false` | Adds `get` on Secrets to the Role. |
| `extraEnv` | `[]` | More environment variables of the server. |
| `service.type` | `ClusterIP` | Service type. |
| `service.port` | `8080` | Service port. |
| `ingress.enabled` | `false` | Creates the Ingress. |
| `ingress.className` | `""` | `ingressClassName`. |
| `ingress.host` | `""` | Host of the Ingress rule. |
| `ingress.annotations` | `{}` | Ingress annotations. |
| `ingress.tls` | `[]` | Ingress TLS blocks. |
| `resources` | `{}` | Resources of the server container. |
| `serviceAccount.name` | `""` | Empty uses the release full name. |
| `serviceAccount.annotations` | `{}` | For example, a workload identity annotation. |
| `podAnnotations` | `{}` | Annotations of the server pods. |
| `nodeSelector`, `tolerations`, `affinity` | empty | Node placement of the server pods. |
| `shutdownGraceSeconds` | `30` | `SLUICE_SHUTDOWN_GRACE`. The pod `terminationGracePeriodSeconds` is 10 s longer. |

The chart also sets `SLUICE_LISTEN_ADDR` to `:8080` and `SLUICE_K8S_NAMESPACE` to the namespace of the pod.

### Secrets for the database and the master keys

The chart reads the database URL and the master keys only from Secrets. It never puts them in the pod spec as plain values. `database.existingSecret` must name a Secret that exists before the install. The default name is `sluice-db` with the key `url`.

A pooled URL works, for example PgBouncer in transaction mode or the Neon pooler. See [Postgres requirements](#postgres-requirements).

### More environment variables with extraEnv

Use `extraEnv` for every variable that has no value of its own, for example storage and Vault variables:

```yaml
storage:
  type: s3
extraEnv:
  - name: SLUICE_S3_BUCKET
    value: sluice
  - name: SLUICE_S3_REGION
    value: eu-central-1
  - name: SLUICE_VAULT_ADDR
    value: https://vault.example.com
  - name: SLUICE_VAULT_K8S_ROLE
    value: sluice
```

`extraEnv` entries are standard container `env` entries, so `valueFrom.secretKeyRef` works too.

### RBAC

The Role and the RoleBinding apply to the release namespace:

| API group | Resource | Verbs | When |
|---|---|---|---|
| `batch` | `jobs` | create, get, list, watch, delete | Always. |
| core | `pods` | get, list, watch | Always. |
| core | `pods/log` | get | Always. |
| core | `secrets` | get | Only with `kubernetesSecretProvider.enabled: true`. |

The Job create dry run of `SLUICE_EXECUTORS=auto` needs the `create` verb on `jobs`. With the chart, `auto` thus enables the kubernetes executor.

### Probes and security

| Item | Value |
|---|---|
| Readiness probe | `GET /readyz`, every 5 s, 3 failures. |
| Liveness probe | `GET /healthz`, every 10 s, 6 failures. |
| Pod security context | `runAsNonRoot`, user and group 65532, `fsGroup` 65532, seccomp `RuntimeDefault`. |
| Container security context | `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`, all capabilities dropped. |
| Writable path | An `emptyDir` at `/tmp`. |

`/readyz` checks the database, the migrations, a storage round trip and the master keys. `/healthz` only tells that the process is alive (REQ-CORE-004). See [operations/runbook.md](operations/runbook.md).

### Replicas

Any number of replicas can serve one database (C-05). They share the queue, and leases select one leader for each background job. See [Multiple instances and leases](#multiple-instances-and-leases).

A task without an executor type runs on the process executor, inside a server pod. The `sluice` image has no shell and no runtimes. Choose one of these setups:

- Set `defaults.executor` in `namespace.yaml` to `type: kubernetes` with a task image. See [executors.md](executors.md#choose-the-executor-of-a-task).
- Use the `sluice-uv` image for the server, so that process tasks have `bash`, `uv`, Python and `bun`.

## Postgres requirements

- Sluice needs one database. It has no other state store (C-02).
- The tests and the compose files use Postgres 17.
- The first migration runs `CREATE EXTENSION IF NOT EXISTS citext`. The database user must have the right to create this extension.
- A transaction-mode pooler works, for example PgBouncer or the Neon pooler (C-06).

Sluice is safe behind a transaction-mode pooler because it uses no session state (DI-5):

| Postgres feature | Sluice |
|---|---|
| Prepared statements | Not used. Queries use unnamed statements (`QueryExecModeExec`). Statement caches are off. |
| Session advisory locks | Not used. Migrations use a transaction-scoped advisory lock. Leader election uses the `leases` table (D-02). |
| `LISTEN` and `NOTIFY` | Not used. Instances poll the queue with `FOR UPDATE SKIP LOCKED`. |
| Temporary tables | Not used. |

Migrations run in the simple query protocol. The test `SCN-CORE-004` creates a user, a managed flow and an execution to `SUCCESS` through PgBouncer in transaction mode, and hands over the `scheduler` lease between two instances.

## Storage drivers

Sluice stores file contents, bundles, logs and artifacts in object storage. `SLUICE_STORAGE_TYPE` selects the driver. The default is `postgres` (D-11).

| Driver | Variables | Notes |
|---|---|---|
| `postgres` | none | Default. Objects go into the `storage_objects` and `storage_chunks` tables in 1 MiB chunks. No other infrastructure. |
| `fs` | `SLUICE_FS_ROOT` | A directory. The chart notes that `fs` needs a shared file system and is not for more than one replica. |
| `s3` | `SLUICE_S3_BUCKET`, `SLUICE_S3_REGION`, `SLUICE_S3_ENDPOINT`, `SLUICE_S3_FORCE_PATH_STYLE`, `SLUICE_S3_ACCESS_KEY_ID`, `SLUICE_S3_SECRET_ACCESS_KEY`, `SLUICE_S3_PREFIX` | AWS S3, Cloudflare R2 and MinIO. Without static keys, the driver uses the default AWS credential chain. |
| `azblob` | `SLUICE_AZBLOB_ACCOUNT_URL`, `SLUICE_AZBLOB_CONNECTION_STRING`, `SLUICE_AZBLOB_CONTAINER`, `SLUICE_AZBLOB_PREFIX` | Azure Blob Storage. An account URL uses `DefaultAzureCredential`. A connection string replaces the account URL. |

Startup rules:

| Driver | Rule |
|---|---|
| `fs` | `SLUICE_FS_ROOT` is required. |
| `s3` | `SLUICE_S3_BUCKET` is required. Set both static keys or neither. `SLUICE_MAX_ARTIFACT_BYTES` and `SLUICE_MAX_BUNDLE_BYTES` must be at most 52428800000 bytes. This is 10 000 parts of 5 MiB, the limit of one upload. |
| `azblob` | `SLUICE_AZBLOB_CONTAINER` is required. Set `SLUICE_AZBLOB_ACCOUNT_URL` or `SLUICE_AZBLOB_CONNECTION_STRING`. |

Azure credentials use the standard `AZURE_*` variables, workload identity or managed identity.

```sh
SLUICE_STORAGE_TYPE=s3
SLUICE_S3_BUCKET=sluice
SLUICE_S3_ENDPOINT=https://<account>.r2.cloudflarestorage.com
SLUICE_S3_REGION=auto
SLUICE_S3_ACCESS_KEY_ID=...
SLUICE_S3_SECRET_ACCESS_KEY=...
SLUICE_S3_PREFIX=prod/
```

### Keys, streams and garbage collection

| Object | Key |
|---|---|
| File content | `files/sha256/<hash>` |
| Bundle | `bundles/<manifest_hash>.tar.gz` |
| Task log | `logs/<execution_id>/<task_run_id>.ndjson.gz` |
| Artifact | `artifacts/<execution_id>/<task_run_id>/<name>` |

- Reads and writes stream. A 150 MiB object is not held fully in memory (REQ-STO-004).
- Sluice stores the same file content once, by its hash (REQ-STO-005).
- The maintenance leader collects garbage at most once in 24 hours (DI-17). It deletes bundles unused for 7 days and file objects that no snapshot references. It also deletes stored files without a row, and logs and artifacts whose execution is gone. It keeps objects younger than 1 hour.

Admins see the driver and its health on **Settings → Storage** (`/settings/storage`) and with `GET /api/v1/storage`. The value comes from the environment. The page cannot change it.

![The Storage settings page. The driver is Postgres and the health is Healthy.](images/storage.png)

| Test | Proves |
|---|---|
| `SCN-STO-001` | One conformance suite on postgres, fs, s3 with MinIO and azblob with Azurite. It includes a 150 MiB stream with heap growth below 64 MiB. |
| `SCN-STO-002` | s3 with endpoint override and path-style addressing. With a prefix, all keys start with it. |
| `SCN-STO-003` | azblob with a connection string and with a token credential. |
| `SCN-STO-004` | The same content in two namespaces creates one object. |
| `SCN-STO-005` | Garbage collection deletes unreferenced objects and keeps referenced ones. |

## Multiple instances and leases

Instances in different clusters can use one database. They cooperate through pools and leases with no extra configuration (REQ-DEP-004). One Sluice deployment is one environment (D-15).

```mermaid
flowchart LR
  subgraph A["Cluster A"]
    SA["sluice server<br/>SLUICE_POOLS=cluster-a"] --> JA["Jobs of pool cluster-a"]
  end
  subgraph B["Cluster B"]
    SB["sluice server<br/>SLUICE_POOLS=cluster-b"] --> JB["Jobs of pool cluster-b"]
  end
  SA --> PG[("Postgres")]
  SB --> PG
  SA --> OS[("Object storage")]
  SB --> OS
```

All instances must use the same database, the same storage and the same master keys. Each instance claims only tasks of its own pools. The pool of a task comes from `executor.pool`. See [executors.md](executors.md#pools).

**Instance registry.** Each instance registers its hostname, version, pools and executors. It writes a heartbeat every 10 s. An instance is offline after 60 s without a heartbeat. Sluice deletes offline rows after 24 hours (REQ-CORE-007).

**Leases.** A lease is a row in the `leases` table with a TTL of 15 s. The holder renews it every 5 s. A leader-only write checks the holder in the same statement (REQ-CORE-006, D-02).

| Lease | Work of the holder |
|---|---|
| `scheduler` | Fires due schedules every second. Each schedule time creates at most one execution (D-14). |
| `maintenance` | Every 2 s: flow and task deadlines, lost tasks of offline instances, `no_instance_for_pool`. Also retention and storage garbage collection. |
| `git-sync` | Polls and syncs git sources. See [git-sync.md](git-sync.md). |
| `k8s-reconcile:<pool>` | One pass every 60 s over the Jobs of the pool. Only instances with the kubernetes executor take it. |

| Test | Proves |
|---|---|
| `SCN-CORE-005` | When the holder stops renewal, another instance holds the lease within 20 s. A leader-only write of the old holder is rejected. |
| `SCN-CORE-006` | Two instances show in `GET /api/v1/instances`. A stopped instance shows offline after 60 s. |
| `SCN-DEP-004` | With the pools `cluster-a` and `cluster-b`, tasks run only on their pool, schedules fire once, and both instances list all executions. |
| `SCN-TRG-004` | Three instances with forced leader changes create exactly one execution per schedule time. |

## Upgrades and migrations

The migrations are embedded in the binary. At start, `sluice server` applies all migrations that the database does not have (REQ-CORE-003). `sluice migrate` applies them and exits.

- All new migrations run in one transaction after `pg_advisory_xact_lock` (DI-2). Concurrent starts apply each migration once. The test `SCN-CORE-002` starts three instances at the same time on an empty database.
- The table `schema_migrations` records the applied versions.
- `/readyz` fails the `migrations` check when the count of applied migrations differs from the count in the binary.

To upgrade:

1. Back up the database.
2. Run `sluice migrate` with the new image. This step is optional, because the server also migrates at start.
3. Roll out the new image.

During a rollout, an old instance that stops fails its process and inline tasks with `instance_shutdown`, and the retry policy applies. Docker and Kubernetes tasks continue (REQ-CORE-008). The shutdown takes at most `SLUICE_SHUTDOWN_GRACE`. After the new migrations apply, `/readyz` of an old instance fails, because its migration count is lower.

> **Warning:** Do not expect schema changes between two pre-release builds. Until the v1 release, the schema is one migration file `00001_init.sql` that changes in place (DI-4). A database that already has version `00001` does not get these changes.

## CLI

The binary is also the CLI (REQ-CORE-001). `sluice help` prints the command list.

| Command | What it does | Needs the database |
|---|---|---|
| `sluice server` | Runs the HTTP server, the scheduler and the executors. Applies migrations at start. | yes, and `SLUICE_PUBLIC_URL` |
| `sluice exec` | Runs one task run. This is the runner. It reads `SLUICE_API_URL`, `SLUICE_RUN_TOKEN` and `SLUICE_TASK_RUN_ID`. | no |
| `sluice runner-install <dir>` | Copies the binary to `<dir>/sluice`. Kubernetes init containers use it. | no |
| `sluice migrate` | Applies the migrations and prints `applied <n> migrations`. | yes |
| `sluice user create` | Creates a user. | yes |
| `sluice user reset-password` | Sets a new password for a user. | yes |
| `sluice secrets rekey` | Encrypts all builtin secrets again with the active master key. | yes, and `SLUICE_MASTER_KEYS` |
| `sluice validate <dir> [--json]` | Validates a namespace directory offline. | no |
| `sluice openapi` | Prints the OpenAPI document of the API. | no |
| `sluice version` | Prints `version`, `commit` and `build_date`. | no |

The commands that need the database read `SLUICE_DATABASE_URL`. `user create`, `user reset-password` and `secrets rekey` apply new migrations first.

| Exit code | Result |
|---|---|
| `0` | Success. For `validate`, the namespace is valid. |
| `1` | The command failed. For `validate`, the namespace is invalid. |
| `2` | Invalid configuration, invalid flags or an unknown command. |

### user create

| Flag | Default | Rule |
|---|---|---|
| `--email` | none | Required. |
| `--name` | empty | Display name. |
| `--role` | `viewer` | `viewer`, `operator`, `editor` or `admin`. |
| `--password` | none | The password. Use this flag or `--password-stdin`. |
| `--password-stdin` | `false` | Reads the password from stdin. |
| `--temporary` | `false` | Forces a password change at the first login. |

A new password needs at least 10 characters (DI-13). Without `--temporary`, the password is final.

```sh
printf '%s' "$NEW_PASSWORD" | sluice user create --email ada@example.com --name Ada --role editor --password-stdin
```

The test `SCN-AUTH-002` creates an editor with this command.

### user reset-password

| Flag | Default | Rule |
|---|---|---|
| `--email` | none | Required. |
| `--password` | none | The new password. Use this flag or `--password-stdin`. |
| `--password-stdin` | `false` | Reads the password from stdin. |
| `--temporary` | `false` | Forces a password change at the next login. |

Use this command to recover access when no admin can sign in:

```sh
printf '%s' "$NEW_PASSWORD" | sluice user reset-password --email admin@example.com --password-stdin
```

### secrets rekey

`sluice secrets rekey` encrypts every builtin secret again with the first key of `SLUICE_MASTER_KEYS`. It prints `re-encrypted <n> secrets with key <kid>`. It stops with exit code 2 when `SLUICE_MASTER_KEYS` is empty.

To rotate the master key:

1. Put the new key first in `SLUICE_MASTER_KEYS` and keep the old key after it.
2. Restart all instances.
3. Run `sluice secrets rekey`.
4. Remove the old key from `SLUICE_MASTER_KEYS`.
5. Restart all instances.

The test `SCN-SEC-006` proves this order. It also proves that `/readyz` fails with `master_key_missing` when you remove the old key before the rekey.

### validate

`sluice validate <dir>` reads a namespace directory and validates all flow files and `namespace.yaml`. The `--json` flag prints the result in the format of `schemas/validate-result.schema.json`. The flag can come before or after the directory. The command skips symlinks and `.git` directories with a warning.

```sh
sluice validate examples/elt/namespace --json
```

The test `SCN-FLOW-006` proves the exit codes and the JSON format. See [flows.md](flows.md).

### version

```sh
$ docker run --rm sluice:dev version
version: dev
commit: unknown
build_date: unknown
```

The Dockerfile sets `version` and `commit` from the build arguments `VERSION` and `COMMIT`. It does not set `build_date`.

## Related documents

- [getting-started.md](getting-started.md): the first start and the first flow.
- [executors.md](executors.md): executors, pools, slots and the runner.
- [architecture.md](architecture.md): the components and the leases.
- [operations/runbook.md](operations/runbook.md): health, logs, backups and common errors.
- [operations/metrics.md](operations/metrics.md): the Prometheus metrics of `/metrics`.
- [operations/security.md](operations/security.md): hardening and the protection of secrets.
- [reference/env.md](reference/env.md): every environment variable.
