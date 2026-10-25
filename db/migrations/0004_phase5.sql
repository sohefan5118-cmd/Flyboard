ALTER TABLE admins ADD COLUMN IF NOT EXISTS enabled boolean NOT NULL DEFAULT true;
ALTER TABLE users ADD COLUMN IF NOT EXISTS traffic_limit bigint NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS traffic_used bigint NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS panel_cidrs(id bigserial PRIMARY KEY,cidr cidr NOT NULL UNIQUE,note text,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS audit_logs(id bigserial PRIMARY KEY,admin_id bigint REFERENCES admins ON DELETE SET NULL,action text NOT NULL,resource text,resource_id bigint,ip inet,detail jsonb NOT NULL DEFAULT '{}',created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS client_owner_locks(client_id bigint NOT NULL REFERENCES clients ON DELETE CASCADE,fingerprint text NOT NULL,ip inet NOT NULL,user_agent text NOT NULL,seen_at timestamptz NOT NULL,PRIMARY KEY(client_id,fingerprint));
CREATE INDEX IF NOT EXISTS audit_logs_created ON audit_logs(created_at DESC);
INSERT INTO settings(key,value) VALUES('panel_whitelist_enabled','false'::jsonb),('owner_lock_enabled','true'::jsonb),('owner_lock_window','120'::jsonb) ON CONFLICT DO NOTHING;
