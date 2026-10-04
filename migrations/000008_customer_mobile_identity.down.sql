-- Restores customer numbers (renumbered C-1001… per company in name order) and
-- the Customer No label. Merged duplicate customers are not restored.
ALTER TABLE service_profiles DROP CONSTRAINT service_profiles_core_labels_check;
UPDATE service_profiles SET core_labels = core_labels || '{"customer_no": "Customer No"}'::jsonb;
ALTER TABLE service_profiles
    ALTER COLUMN core_labels SET DEFAULT '{
        "service_date": "Date",
        "customer_no": "Customer No",
        "customer_name": "Customer Name",
        "customer_contact": "Contact No"
    }'::jsonb,
    ADD CONSTRAINT service_profiles_core_labels_check CHECK (
        jsonb_typeof(core_labels) = 'object'
        AND core_labels ?& ARRAY['service_date', 'customer_no', 'customer_name', 'customer_contact']
        AND core_labels - ARRAY['service_date', 'customer_no', 'customer_name', 'customer_contact'] = '{}'::jsonb
        AND jsonb_typeof(core_labels->'service_date') = 'string'
        AND jsonb_typeof(core_labels->'customer_no') = 'string'
        AND jsonb_typeof(core_labels->'customer_name') = 'string'
        AND jsonb_typeof(core_labels->'customer_contact') = 'string'
        AND btrim(core_labels->>'service_date') <> ''
        AND btrim(core_labels->>'customer_no') <> ''
        AND btrim(core_labels->>'customer_name') <> ''
        AND btrim(core_labels->>'customer_contact') <> ''
        AND char_length(core_labels->>'service_date') <= 200
        AND char_length(core_labels->>'customer_no') <= 200
        AND char_length(core_labels->>'customer_name') <= 200
        AND char_length(core_labels->>'customer_contact') <= 200
    );

ALTER TABLE customers ADD COLUMN customer_no text;
UPDATE customers c SET customer_no = 'C-' || n.number
FROM (SELECT id, 1000 + row_number() OVER (PARTITION BY company_id ORDER BY name, id) AS number FROM customers) n
WHERE c.id = n.id;
ALTER TABLE customers
    ALTER COLUMN customer_no SET NOT NULL,
    ADD CHECK (customer_no ~ '^C-[0-9]+$' AND char_length(customer_no) <= 32),
    ADD UNIQUE (company_id, customer_no);

ALTER TABLE companies ADD COLUMN next_customer_number bigint NOT NULL DEFAULT 1001
    CHECK (next_customer_number BETWEEN 1001 AND 9007199254740991);
UPDATE companies co SET next_customer_number = 1001 + (SELECT count(*) FROM customers c WHERE c.company_id = co.id);

DROP INDEX customers_company_mobile_key;
