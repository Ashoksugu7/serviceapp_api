\set ON_ERROR_STOP on
BEGIN;
SELECT 'serviceops_t33_' || replace(gen_random_uuid()::text, '-', '') AS test_schema \gset
CREATE SCHEMA :"test_schema";
SET LOCAL search_path TO :"test_schema", pg_catalog;
\ir ../migrations/000001_company_master_data.up.sql
\ir ../migrations/000002_service_transactions.up.sql
\ir ../migrations/000003_authentication.up.sql
\ir ../migrations/000004_user_phone.up.sql
\ir ../migrations/000005_must_change_password.up.sql
\ir ../migrations/000006_password_reset_tokens.up.sql
\ir ../migrations/000007_record_history_events.up.sql

-- Duplicates before the migration: C-1001 and C-1003 share a mobile number
-- (different spacing); C-1002 is unique; the other company's same number stays.
DO $$
DECLARE
    company_id uuid; other_company uuid; user_id uuid; profile_id uuid; status_id uuid; item_id uuid;
    keeper uuid; duplicate uuid; request_id uuid;
BEGIN
    INSERT INTO companies(name,code) VALUES ('Company','C') RETURNING id INTO company_id;
    INSERT INTO companies(name,code) VALUES ('Other','O') RETURNING id INTO other_company;
    INSERT INTO users(company_id,name,email,password_hash,role) VALUES (company_id,'User','u@example.test','x','USER') RETURNING id INTO user_id;
    INSERT INTO service_profiles(company_id,name,prefix) VALUES (company_id,'Repair','R') RETURNING id INTO profile_id;
    INSERT INTO service_profile_statuses(company_id,profile_id,name,is_initial) VALUES (company_id,profile_id,'Open',true) RETURNING id INTO status_id;
    INSERT INTO standby_items(company_id,name) VALUES (company_id,'Loaner') RETURNING id INTO item_id;
    INSERT INTO customers(company_id,customer_no,name,contact) VALUES (company_id,'C-1001','Ravi','98765 43210') RETURNING id INTO keeper;
    INSERT INTO customers(company_id,customer_no,name,contact) VALUES (company_id,'C-1002','Meena','91234 56789');
    INSERT INTO customers(company_id,customer_no,name,contact,email,address) VALUES (company_id,'C-1003','Ravi K','9876543210','ravi@example.test','Anna Nagar') RETURNING id INTO duplicate;
    INSERT INTO customers(company_id,customer_no,name,contact) VALUES (other_company,'C-1001','Ravi elsewhere','9876543210');
    INSERT INTO service_requests(company_id,profile_id,request_no,service_date,customer_id,status_id,created_by)
        VALUES (company_id,profile_id,'R1001','2026-10-01',duplicate,status_id,user_id) RETURNING id INTO request_id;
    INSERT INTO standby_item_issues(company_id,standby_item_id,customer_id,service_request_id,issued_date)
        VALUES (company_id,item_id,duplicate,request_id,'2026-10-01');
    INSERT INTO service_request_history(company_id,request_id,field_key,old_value,new_value,changed_by)
        VALUES (company_id,request_id,'core.customer_id',to_jsonb(keeper::text),to_jsonb(duplicate::text),user_id);
    CREATE TEMP TABLE t33_expect AS SELECT keeper, duplicate, company_id;
END $$;

\ir ../migrations/000008_customer_mobile_identity.up.sql

DO $$
DECLARE e record;
BEGIN
    SELECT * INTO e FROM t33_expect;
    IF EXISTS (SELECT 1 FROM customers WHERE id = e.duplicate) THEN RAISE EXCEPTION 'Duplicate customer was not deleted'; END IF;
    IF (SELECT count(*) FROM customers) <> 3 THEN RAISE EXCEPTION 'Expected 3 customers after the merge'; END IF;
    IF (SELECT email FROM customers WHERE id = e.keeper) IS DISTINCT FROM 'ravi@example.test'
       OR (SELECT address FROM customers WHERE id = e.keeper) IS DISTINCT FROM 'Anna Nagar' THEN
        RAISE EXCEPTION 'Kept customer did not take the missing email and address';
    END IF;
    IF EXISTS (SELECT 1 FROM service_requests WHERE customer_id <> e.keeper)
       OR EXISTS (SELECT 1 FROM standby_item_issues WHERE customer_id <> e.keeper) THEN
        RAISE EXCEPTION 'Records and stand-by issues must move to the kept customer';
    END IF;
    IF EXISTS (SELECT 1 FROM service_request_history WHERE new_value <> to_jsonb(e.keeper::text)) THEN
        RAISE EXCEPTION 'History must reference the kept customer';
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema()
               AND ((table_name = 'customers' AND column_name = 'customer_no') OR (table_name = 'companies' AND column_name = 'next_customer_number'))) THEN
        RAISE EXCEPTION 'Customer number columns must be dropped';
    END IF;
    BEGIN
        INSERT INTO customers(company_id,name,contact) VALUES (e.company_id,'Copy','+91-98765-43210'::text);
        -- '+91…' has extra digits, so it is a different number; the exact digits must clash:
        INSERT INTO customers(company_id,name,contact) VALUES (e.company_id,'Copy','98765-43210');
        RAISE EXCEPTION 'Duplicate mobile number unexpectedly succeeded';
    EXCEPTION WHEN unique_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO service_profiles(company_id,name,prefix,core_labels)
            VALUES (e.company_id,'Bad','B','{"service_date":"D","customer_no":"N","customer_name":"C","customer_contact":"M"}');
        RAISE EXCEPTION 'customer_no label unexpectedly accepted';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    IF NOT (SELECT core_labels ? 'customer_contact' AND NOT core_labels ? 'customer_no' FROM service_profiles WHERE name = 'Repair') THEN
        RAISE EXCEPTION 'Existing profile labels must drop customer_no';
    END IF;
END $$;

\ir ../migrations/000009_drop_company_code.up.sql
DO $$
BEGIN
    INSERT INTO companies(name) VALUES ('No code needed');
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'companies' AND column_name = 'code') THEN
        RAISE EXCEPTION 'Company code must be dropped';
    END IF;
END $$;

\ir ../migrations/000009_drop_company_code.down.sql
\ir ../migrations/000008_customer_mobile_identity.down.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM customers WHERE customer_no IS NULL) OR EXISTS (SELECT 1 FROM companies WHERE code IS NULL) THEN
        RAISE EXCEPTION 'Down migrations must restore customer numbers and company codes';
    END IF;
END $$;
\ir ../migrations/000008_customer_mobile_identity.up.sql
\ir ../migrations/000009_drop_company_code.up.sql
ROLLBACK;
\echo 'T33 customer merge, unique mobile, dropped customer number and company code, down and reapply checks passed; all test changes rolled back.'
