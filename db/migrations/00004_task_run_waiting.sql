-- +goose Up
-- A wait task holds its task run in WAITING until an answer comes. No instance claims it.
ALTER TABLE task_runs DROP CONSTRAINT task_runs_state_check;
ALTER TABLE task_runs ADD CONSTRAINT task_runs_state_check
    CHECK (state IN ('PENDING', 'QUEUED', 'RUNNING', 'WAITING', 'SUCCESS', 'FAILED', 'TIMED_OUT', 'CANCELLED', 'SKIPPED'));
ALTER TABLE task_runs ADD COLUMN wait_message text NOT NULL DEFAULT '';
CREATE INDEX task_runs_waiting_idx ON task_runs (execution_id) WHERE state = 'WAITING';
