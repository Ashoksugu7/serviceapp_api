-- T35: history rows written in one transaction (a status change and the
-- Out-Store event that caused it, or several field edits) keep the order they
-- were written in. now() is the transaction start, so they used to tie.
ALTER TABLE service_request_history
    ALTER COLUMN created_at SET DEFAULT clock_timestamp();

-- T36: who lent and who received each stand-by item. Issues recorded before
-- this migration keep NULL (the user is unknown).
ALTER TABLE standby_item_issues
    ADD COLUMN issued_by uuid,
    ADD COLUMN returned_by uuid,
    ADD FOREIGN KEY (company_id, issued_by) REFERENCES users(company_id, id),
    ADD FOREIGN KEY (company_id, returned_by) REFERENCES users(company_id, id),
    ADD CONSTRAINT standby_item_issues_returned_by_check
        CHECK (returned_by IS NULL OR returned_at IS NOT NULL);
