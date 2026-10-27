-- +goose Up
-- An assistant turn runs on the server apart from the browser. Its state is on the
-- conversation, so that every instance can stream it, stop it and find a dead turn.
ALTER TABLE ai_conversations ADD COLUMN turn_status text NOT NULL DEFAULT 'idle'
    CHECK (turn_status IN ('idle', 'running', 'failed'));
ALTER TABLE ai_conversations ADD COLUMN turn_stop text NOT NULL DEFAULT '';
ALTER TABLE ai_conversations ADD COLUMN turn_error text NOT NULL DEFAULT '';
ALTER TABLE ai_conversations ADD COLUMN draft_text text NOT NULL DEFAULT '';
ALTER TABLE ai_conversations ADD COLUMN turn_heartbeat_at timestamptz;
ALTER TABLE ai_conversations ADD COLUMN stop_requested boolean NOT NULL DEFAULT false;
ALTER TABLE ai_conversations ADD COLUMN auto_accept boolean NOT NULL DEFAULT false;
CREATE INDEX ai_conversations_running_idx ON ai_conversations (turn_heartbeat_at) WHERE turn_status = 'running';
-- With accept-all on, a mutating call runs without a confirm and records auto_confirmed.
ALTER TABLE ai_pending_actions DROP CONSTRAINT ai_pending_actions_status_check;
ALTER TABLE ai_pending_actions ADD CONSTRAINT ai_pending_actions_status_check
    CHECK (status IN ('pending', 'confirmed', 'rejected', 'auto_confirmed'));
