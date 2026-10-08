-- +goose Up
-- A task with each has one task run per item. item_index is the position of the item and item
-- is its value. A task without each has item_index 0 and no item, as all rows before this file.
ALTER TABLE task_runs ADD COLUMN item_index integer NOT NULL DEFAULT 0;
ALTER TABLE task_runs ADD COLUMN item jsonb;
ALTER TABLE task_runs DROP CONSTRAINT task_runs_execution_id_task_key_attempt_key;
ALTER TABLE task_runs ADD CONSTRAINT task_runs_item_key UNIQUE (execution_id, task_key, item_index, attempt);
