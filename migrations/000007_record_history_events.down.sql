-- Drops who lent and received each stand-by item and restores the history default.
ALTER TABLE standby_item_issues
    DROP COLUMN returned_by,
    DROP COLUMN issued_by;
ALTER TABLE service_request_history
    ALTER COLUMN created_at SET DEFAULT now();
