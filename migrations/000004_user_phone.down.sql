-- Removes phone sign-in. Users keep signing in with email.
DROP INDEX users_phone_key;
ALTER TABLE users DROP COLUMN phone;
