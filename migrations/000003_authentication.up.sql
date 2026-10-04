-- T05/T06: revocable authentication sessions and shared login rate limits.
-- Apply after 000001. Transaction tables from 000002 are not required.

CREATE TABLE auth_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CHECK (expires_at > created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX auth_sessions_user_active_idx
    ON auth_sessions (user_id, expires_at)
    WHERE revoked_at IS NULL;
CREATE INDEX auth_sessions_expiry_idx ON auth_sessions (expires_at);

CREATE TABLE auth_rate_limits (
    scope text NOT NULL CHECK (scope IN ('EMAIL', 'IP')),
    key_hash bytea NOT NULL CHECK (octet_length(key_hash) = 32),
    window_start timestamptz NOT NULL,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (scope, key_hash, window_start),
    CHECK (expires_at > window_start)
);

CREATE INDEX auth_rate_limits_expiry_idx ON auth_rate_limits (expires_at);
