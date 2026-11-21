-- Each service profile controls whether an admin can add products and services
-- directly while entering a service record.
ALTER TABLE service_profiles
    ADD COLUMN catalog_entry_mode text NOT NULL DEFAULT 'MASTERS_ONLY'
    CHECK (catalog_entry_mode IN ('MASTERS_ONLY', 'SERVICE_ENTRY'));
