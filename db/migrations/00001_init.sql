-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id uuid PRIMARY KEY,
    email citext NOT NULL UNIQUE,
    name text NOT NULL DEFAULT '',
    password_hash text NOT NULL,
    role text NOT NULL CHECK (role IN ('viewer', 'operator', 'editor', 'admin')),
    must_change_password boolean NOT NULL DEFAULT false,
    disabled_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz
);

CREATE TABLE sessions (
    id_hash bytea PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    ip text NOT NULL DEFAULT '',
    user_agent text NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE api_tokens (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name text NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    prefix text NOT NULL,
    role text NOT NULL CHECK (role IN ('viewer', 'operator', 'editor', 'admin')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    last_used_at timestamptz,
    revoked_at timestamptz
);
CREATE INDEX api_tokens_user_idx ON api_tokens (user_id);

CREATE TABLE login_attempts (
    email citext NOT NULL,
    ip text NOT NULL,
    attempted_at timestamptz NOT NULL,
    success boolean NOT NULL
);
CREATE INDEX login_attempts_email_idx ON login_attempts (email, attempted_at);
CREATE INDEX login_attempts_ip_idx ON login_attempts (ip, attempted_at);

CREATE TABLE audit_events (
    id uuid PRIMARY KEY,
    ts timestamptz NOT NULL,
    actor_type text NOT NULL CHECK (actor_type IN ('user', 'token', 'system', 'ai')),
    actor_id text NOT NULL DEFAULT '',
    action text NOT NULL,
    target_type text NOT NULL DEFAULT '',
    target_id text NOT NULL DEFAULT '',
    details jsonb NOT NULL DEFAULT '{}',
    ip text NOT NULL DEFAULT ''
);
CREATE INDEX audit_events_ts_idx ON audit_events (ts DESC, id DESC);
CREATE INDEX audit_events_action_idx ON audit_events (action, ts DESC);
CREATE INDEX audit_events_actor_idx ON audit_events (actor_id, ts DESC);

CREATE TABLE git_sources (
    id uuid PRIMARY KEY,
    name text NOT NULL UNIQUE,
    repo_url text NOT NULL,
    branch text NOT NULL,
    auth_type text NOT NULL CHECK (auth_type IN ('none', 'https_token', 'ssh_key')),
    credential_secret_key text NOT NULL DEFAULT '',
    known_hosts text NOT NULL DEFAULT '',
    poll_interval integer NOT NULL DEFAULT 60 CHECK (poll_interval >= 15),
    webhook_secret_key text NOT NULL DEFAULT '',
    last_synced_sha text NOT NULL DEFAULT '',
    last_sync_at timestamptz,
    last_sync_status text NOT NULL DEFAULT '' CHECK (last_sync_status IN ('', 'running', 'success', 'failed')),
    last_error text NOT NULL DEFAULT '',
    sync_requested_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE namespaces (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    source_type text NOT NULL CHECK (source_type IN ('managed', 'git')),
    git_source_id uuid REFERENCES git_sources (id),
    head_snapshot_id uuid,
    description text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
CREATE UNIQUE INDEX namespaces_name_idx ON namespaces (name) WHERE deleted_at IS NULL;

CREATE TABLE git_mappings (
    git_source_id uuid NOT NULL REFERENCES git_sources (id) ON DELETE CASCADE,
    repo_path text NOT NULL,
    namespace_id uuid NOT NULL UNIQUE REFERENCES namespaces (id),
    PRIMARY KEY (git_source_id, repo_path)
);

CREATE TABLE git_sync_runs (
    id uuid PRIMARY KEY,
    git_source_id uuid NOT NULL REFERENCES git_sources (id) ON DELETE CASCADE,
    started_at timestamptz NOT NULL,
    ended_at timestamptz,
    sha text NOT NULL DEFAULT '',
    status text NOT NULL CHECK (status IN ('running', 'success', 'failed')),
    error text NOT NULL DEFAULT '',
    warnings jsonb NOT NULL DEFAULT '[]',
    snapshots_created integer NOT NULL DEFAULT 0
);
CREATE INDEX git_sync_runs_source_idx ON git_sync_runs (git_source_id, started_at DESC);

CREATE TABLE file_objects (
    hash text PRIMARY KEY,
    size bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE snapshots (
    id uuid PRIMARY KEY,
    namespace_id uuid NOT NULL REFERENCES namespaces (id),
    version integer,
    git_sha text NOT NULL DEFAULT '',
    manifest_hash text NOT NULL,
    message text NOT NULL DEFAULT '',
    created_by uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (namespace_id, version)
);
CREATE INDEX snapshots_ns_idx ON snapshots (namespace_id, created_at DESC);

ALTER TABLE namespaces ADD CONSTRAINT namespaces_head_fk FOREIGN KEY (head_snapshot_id) REFERENCES snapshots (id);

CREATE TABLE snapshot_files (
    snapshot_id uuid NOT NULL REFERENCES snapshots (id) ON DELETE CASCADE,
    path text NOT NULL,
    hash text NOT NULL REFERENCES file_objects (hash),
    size bigint NOT NULL,
    executable boolean NOT NULL DEFAULT false,
    PRIMARY KEY (snapshot_id, path)
);
CREATE INDEX snapshot_files_hash_idx ON snapshot_files (hash);

CREATE TABLE bundles (
    manifest_hash text PRIMARY KEY,
    storage_key text NOT NULL,
    size bigint NOT NULL,
    last_used_at timestamptz NOT NULL
);

CREATE TABLE flows (
    id uuid PRIMARY KEY,
    namespace_id uuid NOT NULL REFERENCES namespaces (id),
    flow_key text NOT NULL,
    path text NOT NULL,
    current_revision_id uuid,
    valid boolean NOT NULL DEFAULT false,
    disabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
CREATE UNIQUE INDEX flows_key_idx ON flows (namespace_id, flow_key) WHERE deleted_at IS NULL;

CREATE TABLE flow_revisions (
    id uuid PRIMARY KEY,
    flow_id uuid NOT NULL REFERENCES flows (id),
    snapshot_id uuid NOT NULL REFERENCES snapshots (id),
    source_hash text NOT NULL,
    source text NOT NULL DEFAULT '',
    path text NOT NULL DEFAULT '',
    definition jsonb,
    errors jsonb NOT NULL DEFAULT '[]',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX flow_revisions_flow_idx ON flow_revisions (flow_id, created_at DESC);

ALTER TABLE flows ADD CONSTRAINT flows_revision_fk FOREIGN KEY (current_revision_id) REFERENCES flow_revisions (id);

CREATE TABLE triggers (
    id uuid PRIMARY KEY,
    flow_id uuid NOT NULL REFERENCES flows (id),
    revision_id uuid NOT NULL REFERENCES flow_revisions (id),
    trigger_key text NOT NULL,
    type text NOT NULL CHECK (type IN ('schedule', 'webhook', 'flow')),
    config jsonb NOT NULL DEFAULT '{}',
    webhook_key_hash bytea,
    next_fire_at timestamptz,
    last_fired_at timestamptz,
    active boolean NOT NULL DEFAULT false,
    UNIQUE (flow_id, trigger_key)
);
CREATE INDEX triggers_next_fire_idx ON triggers (next_fire_at) WHERE active AND type = 'schedule';
CREATE UNIQUE INDEX triggers_webhook_idx ON triggers (webhook_key_hash) WHERE webhook_key_hash IS NOT NULL;

CREATE TABLE executions (
    id uuid PRIMARY KEY,
    namespace_id uuid NOT NULL REFERENCES namespaces (id),
    flow_id uuid REFERENCES flows (id),
    flow_revision_id uuid REFERENCES flow_revisions (id),
    snapshot_id uuid NOT NULL REFERENCES snapshots (id),
    state text NOT NULL CHECK (state IN ('QUEUED', 'RUNNING', 'CANCELLING', 'SUCCESS', 'FAILED', 'TIMED_OUT', 'CANCELLED', 'SKIPPED')),
    trigger_type text NOT NULL CHECK (trigger_type IN ('manual', 'schedule', 'webhook', 'flow', 'file', 'subflow', 'rerun', 'restart')),
    trigger_id uuid,
    scheduled_for timestamptz,
    trigger_payload jsonb NOT NULL DEFAULT '{}',
    definition jsonb NOT NULL DEFAULT '{}',
    inputs jsonb NOT NULL DEFAULT '{}',
    labels jsonb NOT NULL DEFAULT '{}',
    outputs jsonb,
    error text NOT NULL DEFAULT '',
    reason text NOT NULL DEFAULT '',
    parent_execution_id uuid REFERENCES executions (id) ON DELETE SET NULL,
    parent_task_run_id uuid,
    restart_of_id uuid REFERENCES executions (id) ON DELETE SET NULL,
    chain_depth integer NOT NULL DEFAULT 0,
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    started_at timestamptz,
    ended_at timestamptz,
    deadline_at timestamptz,
    duration_ms bigint,
    secret_keys_used text[] NOT NULL DEFAULT '{}',
    log_archived boolean NOT NULL DEFAULT false,
    UNIQUE (trigger_id, scheduled_for)
);
CREATE INDEX executions_created_idx ON executions (created_at DESC, id DESC);
CREATE INDEX executions_flow_idx ON executions (flow_id, created_at DESC);
CREATE INDEX executions_ns_idx ON executions (namespace_id, created_at DESC);
CREATE INDEX executions_active_idx ON executions (state) WHERE state IN ('QUEUED', 'RUNNING', 'CANCELLING');
CREATE INDEX executions_ended_idx ON executions (ended_at) WHERE ended_at IS NOT NULL;
CREATE INDEX executions_labels_idx ON executions USING gin (labels jsonb_path_ops);
CREATE INDEX executions_parent_idx ON executions (parent_execution_id) WHERE parent_execution_id IS NOT NULL;

CREATE TABLE task_runs (
    id uuid PRIMARY KEY,
    execution_id uuid NOT NULL REFERENCES executions (id) ON DELETE CASCADE,
    task_key text NOT NULL,
    task_type text NOT NULL DEFAULT '',
    attempt integer NOT NULL DEFAULT 1,
    state text NOT NULL CHECK (state IN ('PENDING', 'QUEUED', 'RUNNING', 'SUCCESS', 'FAILED', 'TIMED_OUT', 'CANCELLED', 'SKIPPED')),
    reason text NOT NULL DEFAULT '',
    executor_type text NOT NULL DEFAULT '',
    pool text NOT NULL DEFAULT 'default',
    claimed_by uuid,
    external_ref text NOT NULL DEFAULT '',
    run_token_hash bytea,
    token_expires_at timestamptz,
    heartbeat_at timestamptz,
    cancel_requested boolean NOT NULL DEFAULT false,
    not_before timestamptz,
    queued_at timestamptz,
    started_at timestamptz,
    ended_at timestamptz,
    exit_code integer,
    error text NOT NULL DEFAULT '',
    outputs jsonb,
    reused_from_id uuid,
    child_execution_id uuid,
    UNIQUE (execution_id, task_key, attempt)
);
CREATE INDEX task_runs_exec_idx ON task_runs (execution_id, task_key, attempt);
CREATE INDEX task_runs_queue_idx ON task_runs (pool, executor_type, queued_at) WHERE state = 'QUEUED';
CREATE INDEX task_runs_running_idx ON task_runs (claimed_by) WHERE state = 'RUNNING';
CREATE INDEX task_runs_pending_idx ON task_runs (not_before) WHERE state = 'PENDING';
CREATE UNIQUE INDEX task_runs_token_idx ON task_runs (run_token_hash) WHERE run_token_hash IS NOT NULL;

CREATE TABLE log_chunks (
    id bigserial PRIMARY KEY,
    task_run_id uuid NOT NULL REFERENCES task_runs (id) ON DELETE CASCADE,
    execution_id uuid NOT NULL REFERENCES executions (id) ON DELETE CASCADE,
    seq integer NOT NULL,
    first_line bigint NOT NULL,
    line_count integer NOT NULL,
    data bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (task_run_id, seq)
);
CREATE INDEX log_chunks_exec_idx ON log_chunks (execution_id, id);

CREATE TABLE metrics (
    id bigserial PRIMARY KEY,
    execution_id uuid NOT NULL REFERENCES executions (id) ON DELETE CASCADE,
    task_run_id uuid NOT NULL REFERENCES task_runs (id) ON DELETE CASCADE,
    flow_id uuid,
    name text NOT NULL,
    value double precision NOT NULL,
    unit text NOT NULL DEFAULT '',
    tags jsonb NOT NULL DEFAULT '{}',
    ts timestamptz NOT NULL,
    seq integer NOT NULL DEFAULT 0,
    idx integer NOT NULL DEFAULT 0,
    UNIQUE (task_run_id, seq, idx)
);
CREATE INDEX metrics_exec_idx ON metrics (execution_id);
CREATE INDEX metrics_flow_idx ON metrics (flow_id, name, ts);

CREATE TABLE artifacts (
    id uuid PRIMARY KEY,
    execution_id uuid NOT NULL REFERENCES executions (id) ON DELETE CASCADE,
    task_run_id uuid NOT NULL REFERENCES task_runs (id) ON DELETE CASCADE,
    name text NOT NULL,
    storage_key text NOT NULL,
    size bigint NOT NULL,
    content_type text NOT NULL DEFAULT 'application/octet-stream',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (task_run_id, name)
);
CREATE INDEX artifacts_exec_idx ON artifacts (execution_id);

CREATE TABLE secret_providers (
    id uuid PRIMARY KEY,
    name text NOT NULL UNIQUE,
    type text NOT NULL CHECK (type IN ('builtin', 'env', 'kubernetes', 'azure_key_vault', 'vault')),
    config jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE secrets (
    id uuid PRIMARY KEY,
    namespace_id uuid REFERENCES namespaces (id),
    key text NOT NULL,
    provider_id uuid NOT NULL REFERENCES secret_providers (id),
    provider_ref text NOT NULL DEFAULT '',
    ciphertext bytea,
    key_id text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    created_by text NOT NULL DEFAULT '',
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_resolved_at timestamptz,
    UNIQUE NULLS NOT DISTINCT (namespace_id, key)
);

CREATE TABLE variables (
    id uuid PRIMARY KEY,
    namespace_id uuid REFERENCES namespaces (id),
    key text NOT NULL,
    value text NOT NULL,
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (namespace_id, key)
);

CREATE TABLE instances (
    id uuid PRIMARY KEY,
    hostname text NOT NULL,
    version text NOT NULL,
    pools text[] NOT NULL,
    executors text[] NOT NULL,
    started_at timestamptz NOT NULL,
    heartbeat_at timestamptz NOT NULL
);

CREATE TABLE leases (
    name text PRIMARY KEY,
    holder text NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE TABLE settings (
    key text PRIMARY KEY,
    value jsonb NOT NULL,
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ai_conversations (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ai_messages (
    id uuid PRIMARY KEY,
    conversation_id uuid NOT NULL REFERENCES ai_conversations (id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('user', 'assistant', 'tool')),
    content jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ai_messages_conv_idx ON ai_messages (conversation_id, created_at, id);

CREATE TABLE ai_pending_actions (
    id uuid PRIMARY KEY,
    conversation_id uuid NOT NULL REFERENCES ai_conversations (id) ON DELETE CASCADE,
    tool_call_id text NOT NULL DEFAULT '',
    tool text NOT NULL,
    arguments jsonb NOT NULL,
    status text NOT NULL CHECK (status IN ('pending', 'confirmed', 'rejected')),
    decided_by uuid,
    decided_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ai_insights (
    id uuid PRIMARY KEY,
    execution_id uuid NOT NULL REFERENCES executions (id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('triage')),
    status text NOT NULL CHECK (status IN ('pending', 'running', 'done', 'failed')),
    summary text NOT NULL DEFAULT '',
    probable_cause text NOT NULL DEFAULT '',
    evidence jsonb NOT NULL DEFAULT '[]',
    suggested_fix text NOT NULL DEFAULT '',
    confidence text NOT NULL DEFAULT '' CHECK (confidence IN ('', 'low', 'medium', 'high')),
    model text NOT NULL DEFAULT '',
    error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ai_insights_exec_idx ON ai_insights (execution_id, created_at DESC);

CREATE TABLE rate_limits (
    key text NOT NULL,
    window_start timestamptz NOT NULL,
    count integer NOT NULL DEFAULT 0,
    PRIMARY KEY (key, window_start)
);

CREATE TABLE storage_objects (
    key text PRIMARY KEY,
    size bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE storage_chunks (
    key text NOT NULL REFERENCES storage_objects (key) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    idx integer NOT NULL,
    data bytea NOT NULL,
    PRIMARY KEY (key, idx)
);

-- +goose Down
DROP SCHEMA public CASCADE;
CREATE SCHEMA public;
