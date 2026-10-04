-- Destructive rollback. Revert later dependent migrations first; no CASCADE.
DROP TABLE standby_item_issues;
DROP TABLE out_store_entries;
DROP TABLE service_request_history;
DROP TABLE service_requests;
