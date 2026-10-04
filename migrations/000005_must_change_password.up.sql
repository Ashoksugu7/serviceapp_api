-- T28: accounts created or reset by an administrator get a server-generated
-- temporary password and must choose their own before using the API.
ALTER TABLE users
    ADD COLUMN must_change_password boolean NOT NULL DEFAULT false;
