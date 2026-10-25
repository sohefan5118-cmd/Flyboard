CREATE TABLE IF NOT EXISTS owner_locks(user_id bigint NOT NULL REFERENCES users ON DELETE CASCADE,fingerprint text NOT NULL,ip inet NOT NULL,user_agent text NOT NULL,seen_at timestamptz NOT NULL,PRIMARY KEY(user_id,fingerprint));
CREATE TABLE IF NOT EXISTS main_user_whitelist(user_id bigint PRIMARY KEY REFERENCES users ON DELETE CASCADE,enabled boolean NOT NULL DEFAULT true,note text,updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS device_audit(id bigserial PRIMARY KEY,user_id bigint REFERENCES users ON DELETE SET NULL,node_id bigint REFERENCES nodes ON DELETE SET NULL,action text NOT NULL,reason text NOT NULL,ip inet,user_agent text,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS device_display(user_id bigint NOT NULL REFERENCES users ON DELETE CASCADE,node_id bigint NOT NULL REFERENCES nodes ON DELETE CASCADE,ip inet NOT NULL,seen_at timestamptz NOT NULL,PRIMARY KEY(user_id,node_id,ip));
ALTER TABLE users ADD COLUMN IF NOT EXISTS online_count integer NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS config jsonb NOT NULL DEFAULT '{}';
CREATE INDEX IF NOT EXISTS sync_outbox_pending ON sync_outbox(id) WHERE delivered_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS sync_outbox_revision_unique ON sync_outbox(aggregate_type,aggregate_id,revision);
