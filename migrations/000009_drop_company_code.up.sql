-- Companies are identified by their UUID; the free-text company code is dropped.
ALTER TABLE companies DROP COLUMN code;
