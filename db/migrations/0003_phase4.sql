ALTER TABLE sync_outbox ADD COLUMN IF NOT EXISTS attempts integer NOT NULL DEFAULT 0;
ALTER TABLE sync_outbox ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE sync_outbox ADD COLUMN IF NOT EXISTS last_error text;
CREATE INDEX IF NOT EXISTS sync_outbox_retry ON sync_outbox(next_attempt_at,id) WHERE delivered_at IS NULL;
