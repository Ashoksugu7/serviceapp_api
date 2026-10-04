-- T25: optional user phone for sign-in. Stored as digits only (6-20), unique
-- across all users like email, so "98765 43210" and "9876543210" are one login.
ALTER TABLE users
    ADD COLUMN phone text CHECK (phone IS NULL OR phone ~ '^[0-9]{6,20}$');

CREATE UNIQUE INDEX users_phone_key ON users (phone) WHERE phone IS NOT NULL;
