-- T29: single-use password-reset links. Only a SHA-256 hash of the emailed
-- token is stored; links expire and are consumed on use.
CREATE TABLE password_reset_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    CHECK (expires_at > created_at)
);

CREATE INDEX password_reset_tokens_user_open_idx ON password_reset_tokens (user_id) WHERE used_at IS NULL;
