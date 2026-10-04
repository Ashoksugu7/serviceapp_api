-- T04: service transaction tables. Apply after 000001 in one transaction.

CREATE TABLE service_requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id),
    profile_id uuid NOT NULL,
    request_no text NOT NULL CHECK (
        request_no ~ '^[A-Z]{1,10}[0-9]+$'
        AND char_length(request_no) <= 32
    ),
    service_date date NOT NULL,
    customer_id uuid NOT NULL,
    status_id uuid NOT NULL,
    form_data jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(form_data) = 'object'),
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, id),
    UNIQUE (company_id, request_no),
    -- Supports issue-history validation against the request customer.
    UNIQUE (company_id, id, customer_id),
    FOREIGN KEY (company_id, profile_id)
        REFERENCES service_profiles(company_id, id),
    FOREIGN KEY (company_id, customer_id)
        REFERENCES customers(company_id, id),
    FOREIGN KEY (company_id, profile_id, status_id)
        REFERENCES service_profile_statuses(company_id, profile_id, id),
    -- SUPER_ADMIN has no company_id and cannot author transactions.
    FOREIGN KEY (company_id, created_by)
        REFERENCES users(company_id, id)
);

CREATE INDEX service_requests_filter_idx
    ON service_requests (company_id, profile_id, status_id, service_date);
CREATE INDEX service_requests_customer_idx
    ON service_requests (company_id, customer_id);
CREATE INDEX service_requests_creator_idx
    ON service_requests (company_id, created_by);
CREATE INDEX service_requests_form_data_idx
    ON service_requests USING gin (form_data);

CREATE TABLE service_request_history (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id),
    request_id uuid NOT NULL,
    field_key text NOT NULL CHECK (
        field_key ~ '^(core\.)?[a-z][a-z0-9_]{0,63}$'
    ),
    old_value jsonb,
    new_value jsonb,
    changed_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, id),
    FOREIGN KEY (company_id, request_id)
        REFERENCES service_requests(company_id, id),
    FOREIGN KEY (company_id, changed_by)
        REFERENCES users(company_id, id)
);

CREATE INDEX service_request_history_request_time_idx
    ON service_request_history (request_id, created_at);
CREATE INDEX service_request_history_company_request_idx
    ON service_request_history (company_id, request_id);
CREATE INDEX service_request_history_actor_idx
    ON service_request_history (company_id, changed_by);

CREATE TABLE out_store_entries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id),
    service_request_id uuid NOT NULL,
    shop_id uuid NOT NULL,
    sent_date date NOT NULL,
    due_date date,
    price numeric(12,2) CHECK (price >= 0),
    remarks text CHECK (remarks IS NULL OR char_length(remarks) <= 4000),
    status text NOT NULL DEFAULT 'SENT'
        CHECK (status IN ('SENT', 'RECEIVED_BACK')),
    received_back_at timestamptz,
    UNIQUE (company_id, id),
    FOREIGN KEY (company_id, service_request_id)
        REFERENCES service_requests(company_id, id),
    FOREIGN KEY (company_id, shop_id)
        REFERENCES out_store_shops(company_id, id),
    CHECK (due_date IS NULL OR due_date >= sent_date),
    CHECK (
        (status = 'SENT' AND received_back_at IS NULL)
        OR (status = 'RECEIVED_BACK' AND received_back_at IS NOT NULL)
    )
);

CREATE INDEX out_store_entries_company_status_idx
    ON out_store_entries (company_id, status);
CREATE INDEX out_store_entries_request_idx
    ON out_store_entries (company_id, service_request_id);
CREATE INDEX out_store_entries_shop_idx
    ON out_store_entries (company_id, shop_id);
CREATE UNIQUE INDEX out_store_entries_one_open_idx
    ON out_store_entries (company_id, service_request_id)
    WHERE status = 'SENT';

CREATE TABLE standby_item_issues (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id),
    standby_item_id uuid NOT NULL,
    customer_id uuid NOT NULL,
    service_request_id uuid,
    received_product_id uuid,
    issued_date date NOT NULL,
    due_date date,
    returned_at timestamptz,
    notes text CHECK (notes IS NULL OR char_length(notes) <= 4000),
    UNIQUE (company_id, id),
    FOREIGN KEY (company_id, standby_item_id)
        REFERENCES standby_items(company_id, id),
    FOREIGN KEY (company_id, customer_id)
        REFERENCES customers(company_id, id),
    FOREIGN KEY (company_id, received_product_id)
        REFERENCES products(company_id, id),
    -- A linked request must belong to the issue customer.
    FOREIGN KEY (company_id, service_request_id, customer_id)
        REFERENCES service_requests(company_id, id, customer_id),
    CHECK (due_date IS NULL OR due_date >= issued_date)
);

CREATE INDEX standby_item_issues_item_idx
    ON standby_item_issues (company_id, standby_item_id, issued_date);
CREATE INDEX standby_item_issues_customer_idx
    ON standby_item_issues (company_id, customer_id);
CREATE INDEX standby_item_issues_request_idx
    ON standby_item_issues (company_id, service_request_id);
CREATE INDEX standby_item_issues_product_idx
    ON standby_item_issues (company_id, received_product_id);
CREATE UNIQUE INDEX standby_item_issues_one_open_idx
    ON standby_item_issues (company_id, standby_item_id)
    WHERE returned_at IS NULL;
