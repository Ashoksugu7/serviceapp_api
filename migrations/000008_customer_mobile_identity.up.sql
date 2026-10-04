-- T33: customers are identified by mobile number; the C-… customer number is
-- dropped. Mobile numbers compare by digits only ("98765 43210" = "9876543210").
--
-- Existing duplicates are merged first: per company and mobile number the
-- oldest customer (lowest customer number) is kept, takes any email/address it
-- is missing from the others, and receives their service records, stand-by
-- issues and history references. The other duplicates are then deleted.
-- This cannot be undone by the down migration.

CREATE TEMP TABLE t33_customer_merge AS
SELECT id AS duplicate_id, keeper_id
FROM (
    SELECT id,
           regexp_replace(contact, '[^0-9]', '', 'g') AS digits,
           first_value(id) OVER (
               PARTITION BY company_id, regexp_replace(contact, '[^0-9]', '', 'g')
               ORDER BY substring(customer_no FROM 3)::numeric, id
           ) AS keeper_id
    FROM customers
) ranked
WHERE id <> keeper_id AND digits <> '';

UPDATE customers k
SET email = COALESCE(k.email, d.email),
    address = COALESCE(k.address, d.address)
FROM (
    SELECT m.keeper_id, min(c.email) AS email, min(c.address) AS address
    FROM t33_customer_merge m
    JOIN customers c ON c.id = m.duplicate_id
    GROUP BY m.keeper_id
) d
WHERE k.id = d.keeper_id;

-- One statement, because a linked stand-by issue must match its record's
-- customer (checked at the end of the statement).
WITH moved_requests AS (
    UPDATE service_requests r SET customer_id = m.keeper_id
    FROM t33_customer_merge m WHERE r.customer_id = m.duplicate_id
    RETURNING r.id
)
UPDATE standby_item_issues i SET customer_id = m.keeper_id
FROM t33_customer_merge m WHERE i.customer_id = m.duplicate_id;

UPDATE service_request_history h SET old_value = to_jsonb(m.keeper_id::text)
FROM t33_customer_merge m
WHERE h.field_key = 'core.customer_id' AND h.old_value = to_jsonb(m.duplicate_id::text);

UPDATE service_request_history h SET new_value = to_jsonb(m.keeper_id::text)
FROM t33_customer_merge m
WHERE h.field_key = 'core.customer_id' AND h.new_value = to_jsonb(m.duplicate_id::text);

DO $$
DECLARE merged bigint;
BEGIN
    SELECT count(*) INTO merged FROM t33_customer_merge;
    RAISE NOTICE 'T33: merged % duplicate customer(s) into the customer with the same mobile number', merged;
END $$;

DELETE FROM customers c USING t33_customer_merge m WHERE c.id = m.duplicate_id;
DROP TABLE t33_customer_merge;

-- One customer per mobile number in each company. The API requires digits in
-- every new mobile number; legacy rows without digits are left unconstrained.
CREATE UNIQUE INDEX customers_company_mobile_key
    ON customers (company_id, regexp_replace(contact, '[^0-9]', '', 'g'))
    WHERE regexp_replace(contact, '[^0-9]', '', 'g') <> '';

ALTER TABLE customers DROP COLUMN customer_no;
ALTER TABLE companies DROP COLUMN next_customer_number;

-- Core form fields become date, customer name and mobile number.
ALTER TABLE service_profiles DROP CONSTRAINT service_profiles_core_labels_check;
UPDATE service_profiles SET core_labels = core_labels - 'customer_no';
ALTER TABLE service_profiles
    ALTER COLUMN core_labels SET DEFAULT '{
        "service_date": "Date",
        "customer_name": "Customer Name",
        "customer_contact": "Mobile No"
    }'::jsonb,
    ADD CONSTRAINT service_profiles_core_labels_check CHECK (
        jsonb_typeof(core_labels) = 'object'
        AND core_labels ?& ARRAY['service_date', 'customer_name', 'customer_contact']
        AND core_labels - ARRAY['service_date', 'customer_name', 'customer_contact'] = '{}'::jsonb
        AND jsonb_typeof(core_labels->'service_date') = 'string'
        AND jsonb_typeof(core_labels->'customer_name') = 'string'
        AND jsonb_typeof(core_labels->'customer_contact') = 'string'
        AND btrim(core_labels->>'service_date') <> ''
        AND btrim(core_labels->>'customer_name') <> ''
        AND btrim(core_labels->>'customer_contact') <> ''
        AND char_length(core_labels->>'service_date') <= 200
        AND char_length(core_labels->>'customer_name') <= 200
        AND char_length(core_labels->>'customer_contact') <= 200
    );
