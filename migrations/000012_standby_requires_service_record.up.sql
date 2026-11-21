-- Keep legacy unlinked history readable, but require every new stand-by loan
-- to be associated with a service record.
ALTER TABLE standby_item_issues
    ADD CONSTRAINT standby_item_issues_service_request_required
    CHECK (service_request_id IS NOT NULL) NOT VALID;
