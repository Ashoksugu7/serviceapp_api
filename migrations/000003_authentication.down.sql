-- Destructive rollback. Revokes all current logins by removing session data.
DROP TABLE auth_rate_limits;
DROP TABLE auth_sessions;
