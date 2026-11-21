-- Automatic status tagging rules for Service Entry.
CREATE TABLE service_profile_workflow_rules (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id),
    profile_id uuid NOT NULL,
    status_id uuid NOT NULL,
    staff_role_id uuid NOT NULL,
    condition_field_key text,
    condition_value jsonb,
    priority integer NOT NULL DEFAULT 0 CHECK (priority >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, id),
    UNIQUE (company_id, profile_id, status_id),
    CHECK ((condition_field_key IS NULL) = (condition_value IS NULL)),
    FOREIGN KEY (company_id, profile_id) REFERENCES service_profiles(company_id, id) ON DELETE CASCADE,
    FOREIGN KEY (company_id, status_id) REFERENCES service_profile_statuses(company_id, id) ON DELETE CASCADE,
    FOREIGN KEY (company_id, staff_role_id) REFERENCES staff_roles(company_id, id)
);

CREATE INDEX service_profile_workflow_rules_match_idx
    ON service_profile_workflow_rules (company_id, profile_id, priority, status_id);
