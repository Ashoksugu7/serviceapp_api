-- Users who still have a temporary password keep it and are no longer forced to change it.
ALTER TABLE users DROP COLUMN must_change_password;
