-- T03: company and master data. Execute the complete file in one transaction.
-- UUIDs use PostgreSQL 14+ built-in gen_random_uuid(); no extension required.

CREATE TABLE companies (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL CHECK (btrim(name) <> '' AND char_length(name) <= 200),
    code text NOT NULL CHECK (btrim(code) <> '' AND char_length(code) <= 32),
    email text CHECK (email IS NULL OR char_length(email) <= 254),
    contact text CHECK (contact IS NULL OR char_length(contact) <= 50),
    address text CHECK (address IS NULL OR char_length(address) <= 4000),
    status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'SUSPENDED')),
    next_customer_number bigint NOT NULL DEFAULT 1001
        CHECK (next_customer_number BETWEEN 1001 AND 9007199254740991),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid REFERENCES companies(id),
    name text NOT NULL CHECK (btrim(name) <> '' AND char_length(name) <= 200),
    email text NOT NULL CHECK (
        btrim(email) <> '' AND char_length(email) <= 254
        AND email = lower(btrim(email))
    ),
    password_hash text NOT NULL CHECK (btrim(password_hash) <> ''),
    role text NOT NULL CHECK (role IN ('SUPER_ADMIN', 'ADMIN', 'USER')),
    status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),
    UNIQUE (company_id, id),
    CHECK ((role = 'SUPER_ADMIN' AND company_id IS NULL) OR (role IN ('ADMIN', 'USER') AND company_id IS NOT NULL))
);

CREATE UNIQUE INDEX users_email_ci_key ON users (lower(email));

CREATE TABLE customers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id),
    customer_no text NOT NULL CHECK (customer_no ~ '^C-[0-9]+$' AND char_length(customer_no) <= 32),
    name text NOT NULL CHECK (btrim(name) <> '' AND char_length(name) <= 200),
    contact text NOT NULL CHECK (btrim(contact) <> '' AND char_length(contact) <= 50),
    email text CHECK (email IS NULL OR char_length(email) <= 254),
    address text CHECK (address IS NULL OR char_length(address) <= 4000),
    UNIQUE (company_id, id),
    UNIQUE (company_id, customer_no)
);

CREATE INDEX customers_company_name_idx ON customers (company_id, name);
CREATE INDEX customers_company_contact_idx ON customers (company_id, contact);

CREATE TABLE staff (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id),
    name text NOT NULL CHECK (btrim(name) <> '' AND char_length(name) <= 200),
    contact text NOT NULL CHECK (btrim(contact) <> '' AND char_length(contact) <= 50),
    email text CHECK (email IS NULL OR char_length(email) <= 254),
    specialization text CHECK (specialization IS NULL OR char_length(specialization) <= 200),
    status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),
    UNIQUE (company_id, id)
);

CREATE TABLE staff_roles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id),
    name text NOT NULL CHECK (btrim(name) <> '' AND char_length(name) <= 200),
    is_system boolean NOT NULL DEFAULT false,
    UNIQUE (company_id, id)
);

CREATE TABLE staff_role_assignments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id),
    staff_id uuid NOT NULL,
    role_id uuid NOT NULL,
    UNIQUE (company_id, id),
    UNIQUE (company_id, staff_id, role_id),
    FOREIGN KEY (company_id, staff_id) REFERENCES staff(company_id, id),
    FOREIGN KEY (company_id, role_id) REFERENCES staff_roles(company_id, id)
);

CREATE INDEX staff_role_assignments_role_idx ON staff_role_assignments (company_id, role_id);

CREATE TABLE service_profiles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id),
    name text NOT NULL CHECK (btrim(name) <> '' AND char_length(name) <= 200),
    prefix text NOT NULL CHECK (prefix ~ '^[A-Z]{1,10}$'),
    next_number bigint NOT NULL DEFAULT 1001
        CHECK (next_number BETWEEN 1001 AND 9007199254740991),
    core_labels jsonb NOT NULL DEFAULT '{
        "service_date": "Date",
        "customer_no": "Customer No",
        "customer_name": "Customer Name",
        "customer_contact": "Contact No"
    }'::jsonb CHECK (
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
    ),
    out_store_enabled boolean NOT NULL DEFAULT false,
    sent_status_id uuid,
    received_status_id uuid,
    is_active boolean NOT NULL DEFAULT true,
    UNIQUE (company_id, id),
    UNIQUE (company_id, prefix)
);

CREATE TABLE service_profile_fields (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id),
    profile_id uuid NOT NULL,
    field_key text NOT NULL CHECK (field_key ~ '^[a-z][a-z0-9_]{0,63}$'),
    label text NOT NULL CHECK (btrim(label) <> '' AND char_length(label) <= 200),
    field_type text NOT NULL CHECK (field_type IN ('text', 'number', 'date', 'choice', 'checkbox', 'linked_product', 'linked_charges', 'staff_role')),
    required boolean NOT NULL DEFAULT false,
    enabled boolean NOT NULL DEFAULT true,
    is_system boolean NOT NULL DEFAULT false,
    sort_order integer NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    config jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(config) = 'object'),
    UNIQUE (company_id, id),
    FOREIGN KEY (company_id, profile_id) REFERENCES service_profiles(company_id, id),
    UNIQUE (profile_id, field_key)
);

CREATE INDEX service_profile_fields_order_idx ON service_profile_fields (company_id, profile_id, sort_order);

CREATE TABLE service_profile_statuses (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id),
    profile_id uuid NOT NULL,
    name text NOT NULL CHECK (btrim(name) <> '' AND char_length(name) <= 200),
    sort_order integer NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    is_initial boolean NOT NULL DEFAULT false,
    is_closed boolean NOT NULL DEFAULT false,
    enabled boolean NOT NULL DEFAULT true,
    UNIQUE (company_id, id),
    FOREIGN KEY (company_id, profile_id) REFERENCES service_profiles(company_id, id),
    UNIQUE (company_id, profile_id, id)
);

CREATE INDEX service_profile_statuses_order_idx ON service_profile_statuses (company_id, profile_id, sort_order);
CREATE UNIQUE INDEX service_profile_statuses_one_initial_idx
    ON service_profile_statuses (company_id, profile_id)
    WHERE is_initial AND enabled;

-- Install circular references after both tables exist. Nullable mappings permit
-- creating a profile first, then statuses, then setting its mappings.
ALTER TABLE service_profiles
    ADD CONSTRAINT service_profiles_sent_status_fk
        FOREIGN KEY (company_id, id, sent_status_id)
        REFERENCES service_profile_statuses(company_id, profile_id, id),
    ADD CONSTRAINT service_profiles_received_status_fk
        FOREIGN KEY (company_id, id, received_status_id)
        REFERENCES service_profile_statuses(company_id, profile_id, id);

CREATE TABLE products (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id),
    profile_id uuid NOT NULL,
    name text NOT NULL CHECK (btrim(name) <> '' AND char_length(name) <= 200),
    brand text CHECK (brand IS NULL OR char_length(brand) <= 200),
    category text CHECK (category IS NULL OR char_length(category) <= 200),
    status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'DISCONTINUED')),
    UNIQUE (company_id, id),
    FOREIGN KEY (company_id, profile_id) REFERENCES service_profiles(company_id, id)
);

CREATE INDEX products_profile_idx ON products (company_id, profile_id);

CREATE TABLE charges (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id),
    profile_id uuid NOT NULL,
    name text NOT NULL CHECK (btrim(name) <> '' AND char_length(name) <= 200),
    description text CHECK (description IS NULL OR char_length(description) <= 4000),
    status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),
    UNIQUE (company_id, id),
    FOREIGN KEY (company_id, profile_id) REFERENCES service_profiles(company_id, id)
);

CREATE INDEX charges_profile_idx ON charges (company_id, profile_id);

CREATE TABLE out_store_shops (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id),
    profile_id uuid,
    shop_name text NOT NULL CHECK (btrim(shop_name) <> '' AND char_length(shop_name) <= 200),
    contact_person text CHECK (contact_person IS NULL OR char_length(contact_person) <= 200),
    contact text CHECK (contact IS NULL OR char_length(contact) <= 50),
    address text CHECK (address IS NULL OR char_length(address) <= 4000),
    status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),
    UNIQUE (company_id, id),
    FOREIGN KEY (company_id, profile_id) REFERENCES service_profiles(company_id, id)
);

CREATE INDEX out_store_shops_profile_idx ON out_store_shops (company_id, profile_id);

CREATE TABLE standby_items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id),
    name text NOT NULL CHECK (btrim(name) <> '' AND char_length(name) <= 200),
    serial_no text CHECK (serial_no IS NULL OR char_length(serial_no) <= 200),
    category text CHECK (category IS NULL OR char_length(category) <= 200),
    price numeric(12,2) CHECK (price >= 0),
    status text NOT NULL DEFAULT 'AVAILABLE'
        CHECK (status IN ('AVAILABLE', 'ISSUED', 'UNDER_MAINTENANCE')),
    UNIQUE (company_id, id)
);

-- Historical JSON data depends on the field key remaining stable.
CREATE FUNCTION protect_service_profile_field_key() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.field_key IS DISTINCT FROM OLD.field_key THEN
        RAISE EXCEPTION 'service profile field_key is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER service_profile_field_key_immutable
BEFORE UPDATE OF field_key ON service_profile_fields
FOR EACH ROW EXECUTE FUNCTION protect_service_profile_field_key();
