\set ON_ERROR_STOP on
BEGIN;
-- All objects live in a unique schema inside this transaction. Disconnect or
-- failure rolls back as well, leaving the application database unchanged.
SELECT 'serviceops_t04_' || replace(gen_random_uuid()::text, '-', '') AS test_schema \gset
CREATE SCHEMA :"test_schema";
SET LOCAL search_path TO :"test_schema", pg_catalog;
\ir ../migrations/000001_company_master_data.up.sql
\ir ../migrations/000002_service_transactions.up.sql

CREATE FUNCTION pg_temp.expect_error(statement text, expected_state text) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE actual_state text;
BEGIN
    BEGIN
        EXECUTE statement;
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS actual_state = RETURNED_SQLSTATE;
        IF actual_state = expected_state THEN RETURN; END IF;
        RAISE EXCEPTION 'Expected SQLSTATE %, received % for %', expected_state, actual_state, statement;
    END;
    RAISE EXCEPTION 'Statement unexpectedly succeeded: %', statement;
END;
$$;

DO $$
DECLARE
    company_a uuid; company_b uuid;
    user_a uuid; user_b uuid; super_admin uuid;
    customer_a uuid; customer_a2 uuid; customer_b uuid;
    profile_a uuid; profile_a2 uuid; profile_b uuid;
    status_a uuid; status_a2 uuid; status_b uuid;
    request_a uuid; request_b uuid;
    shop_a uuid; shop_b uuid;
    item_a uuid; item_b uuid;
    product_a uuid; product_a2 uuid; product_b uuid;
    entry_a uuid; issue_a uuid;
BEGIN
    IF (SELECT count(*) FROM information_schema.tables
        WHERE table_schema=current_schema() AND table_type='BASE TABLE') <> 17 THEN
        RAISE EXCEPTION 'Expected 17 tables after T04';
    END IF;
    IF (SELECT count(*) FROM information_schema.table_constraints
        WHERE table_schema=current_schema() AND constraint_type='PRIMARY KEY') <> 17 THEN
        RAISE EXCEPTION 'Every table must have a primary key';
    END IF;
    IF (SELECT count(*) FROM information_schema.columns
        WHERE table_schema=current_schema() AND column_name='company_id'
          AND data_type='uuid' AND is_nullable='NO') <> 15 THEN
        RAISE EXCEPTION 'Every company-owned table must have a required tenant UUID';
    END IF;
    IF (SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema()
        AND indexname IN (
            'service_requests_filter_idx', 'service_requests_form_data_idx',
            'service_request_history_request_time_idx',
            'out_store_entries_company_status_idx', 'out_store_entries_one_open_idx',
            'standby_item_issues_one_open_idx', 'standby_item_issues_product_idx'
        )) <> 7 THEN RAISE EXCEPTION 'Required T04 indexes are missing'; END IF;

    INSERT INTO companies(name,code) VALUES ('A','A') RETURNING id INTO company_a;
    INSERT INTO companies(name,code) VALUES ('B','B') RETURNING id INTO company_b;
    INSERT INTO users(name,email,password_hash,role) VALUES
        ('Root','root@example.test','hash','SUPER_ADMIN') RETURNING id INTO super_admin;
    INSERT INTO users(company_id,name,email,password_hash,role) VALUES
        (company_a,'User A','a@example.test','hash','USER') RETURNING id INTO user_a;
    INSERT INTO users(company_id,name,email,password_hash,role) VALUES
        (company_b,'User B','b@example.test','hash','USER') RETURNING id INTO user_b;
    INSERT INTO customers(company_id,customer_no,name,contact) VALUES
        (company_a,'C-1001','Customer A','111') RETURNING id INTO customer_a;
    INSERT INTO customers(company_id,customer_no,name,contact) VALUES
        (company_a,'C-1002','Customer A2','112') RETURNING id INTO customer_a2;
    INSERT INTO customers(company_id,customer_no,name,contact) VALUES
        (company_b,'C-1001','Customer B','222') RETURNING id INTO customer_b;

    INSERT INTO service_profiles(company_id,name,prefix) VALUES
        (company_a,'Repair','PC') RETURNING id INTO profile_a;
    INSERT INTO service_profiles(company_id,name,prefix) VALUES
        (company_a,'Other','OT') RETURNING id INTO profile_a2;
    INSERT INTO service_profiles(company_id,name,prefix) VALUES
        (company_b,'Repair','PC') RETURNING id INTO profile_b;
    INSERT INTO service_profile_statuses(company_id,profile_id,name,is_initial) VALUES
        (company_a,profile_a,'Pending',true) RETURNING id INTO status_a;
    INSERT INTO service_profile_statuses(company_id,profile_id,name,is_initial) VALUES
        (company_a,profile_a2,'Pending',true) RETURNING id INTO status_a2;
    INSERT INTO service_profile_statuses(company_id,profile_id,name,is_initial) VALUES
        (company_b,profile_b,'Pending',true) RETURNING id INTO status_b;

    INSERT INTO service_requests(company_id,profile_id,request_no,service_date,customer_id,status_id,form_data,created_by)
        VALUES (company_a,profile_a,'PC1001','2026-09-16',customer_a,status_a,
                '{"confirmed":false,"amount":0}'::jsonb,user_a)
        RETURNING id INTO request_a;
    INSERT INTO service_requests(company_id,profile_id,request_no,service_date,customer_id,status_id,created_by)
        VALUES (company_b,profile_b,'PC1001','2026-09-16',customer_b,status_b,user_b)
        RETURNING id INTO request_b;
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO service_requests(company_id,profile_id,request_no,service_date,customer_id,status_id,created_by) VALUES (%L,%L,''PC1001'',''2026-09-16'',%L,%L,%L)',
        company_a,profile_a,customer_a,status_a,user_a), '23505');
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO service_requests(company_id,profile_id,request_no,service_date,customer_id,status_id,created_by) VALUES (%L,%L,''bad-1'',''2026-09-16'',%L,%L,%L)',
        company_a,profile_a,customer_a,status_a,user_a), '23514');
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO service_requests(company_id,profile_id,request_no,service_date,customer_id,status_id,created_by) VALUES (%L,%L,''PC1002'',''2026-09-16'',%L,%L,%L)',
        company_a,profile_a,customer_a,status_a2,user_a), '23503');
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO service_requests(company_id,profile_id,request_no,service_date,customer_id,status_id,created_by) VALUES (%L,%L,''PC1002'',''2026-09-16'',%L,%L,%L)',
        company_a,profile_a,customer_b,status_a,user_a), '23503');
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO service_requests(company_id,profile_id,request_no,service_date,customer_id,status_id,created_by) VALUES (%L,%L,''PC1002'',''2026-09-16'',%L,%L,%L)',
        company_a,profile_a,customer_a,status_a,user_b), '23503');
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO service_requests(company_id,profile_id,request_no,service_date,customer_id,status_id,created_by) VALUES (%L,%L,''PC1002'',''2026-09-16'',%L,%L,%L)',
        company_a,profile_a,customer_a,status_a,super_admin), '23503');
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO service_requests(company_id,profile_id,request_no,service_date,customer_id,status_id,form_data,created_by) VALUES (%L,%L,''PC1002'',''2026-09-16'',%L,%L,''[]''::jsonb,%L)',
        company_a,profile_a,customer_a,status_a,user_a), '23514');

    INSERT INTO service_request_history(company_id,request_id,field_key,old_value,new_value,changed_by)
        VALUES (company_a,request_a,'core.status','false'::jsonb,'0'::jsonb,user_a);
    IF NOT EXISTS (SELECT 1 FROM service_request_history
        WHERE request_id=request_a AND old_value='false'::jsonb AND new_value='0'::jsonb) THEN
        RAISE EXCEPTION 'History did not preserve false and zero';
    END IF;
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO service_request_history(company_id,request_id,field_key,changed_by) VALUES (%L,%L,''Bad Key'',%L)',
        company_a,request_a,user_a), '23514');
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO service_request_history(company_id,request_id,field_key,changed_by) VALUES (%L,%L,''status'',%L)',
        company_a,request_b,user_a), '23503');
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO service_request_history(company_id,request_id,field_key,changed_by) VALUES (%L,%L,''status'',%L)',
        company_a,request_a,user_b), '23503');

    INSERT INTO out_store_shops(company_id,shop_name) VALUES
        (company_a,'Shop A') RETURNING id INTO shop_a;
    INSERT INTO out_store_shops(company_id,shop_name) VALUES
        (company_b,'Shop B') RETURNING id INTO shop_b;
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO out_store_entries(company_id,service_request_id,shop_id,sent_date) VALUES (%L,%L,%L,''2026-09-17'')',
        company_a,request_a,shop_b), '23503');
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO out_store_entries(company_id,service_request_id,shop_id,sent_date,due_date) VALUES (%L,%L,%L,''2026-09-17'',''2026-09-16'')',
        company_a,request_a,shop_a), '23514');
    INSERT INTO out_store_entries(company_id,service_request_id,shop_id,sent_date,due_date)
        VALUES (company_a,request_a,shop_a,'2026-09-16','2026-09-20') RETURNING id INTO entry_a;
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO out_store_entries(company_id,service_request_id,shop_id,sent_date) VALUES (%L,%L,%L,''2026-09-17'')',
        company_a,request_a,shop_a), '23505');
    PERFORM pg_temp.expect_error(format(
        'UPDATE out_store_entries SET status=''RECEIVED_BACK'' WHERE id=%L', entry_a), '23514');
    UPDATE out_store_entries SET status='RECEIVED_BACK',received_back_at=now() WHERE id=entry_a;
    INSERT INTO out_store_entries(company_id,service_request_id,shop_id,sent_date)
        VALUES (company_a,request_a,shop_a,'2026-09-18');

    INSERT INTO products(company_id,profile_id,name) VALUES
        (company_a,profile_a,'Product A') RETURNING id INTO product_a;
    INSERT INTO products(company_id,profile_id,name) VALUES
        (company_a,profile_a2,'Product A2') RETURNING id INTO product_a2;
    INSERT INTO products(company_id,profile_id,name) VALUES
        (company_b,profile_b,'Product B') RETURNING id INTO product_b;
    INSERT INTO standby_items(company_id,name) VALUES
        (company_a,'Item A') RETURNING id INTO item_a;
    INSERT INTO standby_items(company_id,name) VALUES
        (company_b,'Item B') RETURNING id INTO item_b;
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO standby_item_issues(company_id,standby_item_id,customer_id,issued_date) VALUES (%L,%L,%L,''2026-09-17'')',
        company_a,item_b,customer_a), '23503');
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO standby_item_issues(company_id,standby_item_id,customer_id,service_request_id,issued_date) VALUES (%L,%L,%L,%L,''2026-09-17'')',
        company_a,item_a,customer_a2,request_a), '23503');
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO standby_item_issues(company_id,standby_item_id,customer_id,received_product_id,issued_date) VALUES (%L,%L,%L,%L,''2026-09-17'')',
        company_a,item_a,customer_a,product_b), '23503');
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO standby_item_issues(company_id,standby_item_id,customer_id,issued_date,due_date) VALUES (%L,%L,%L,''2026-09-17'',''2026-09-16'')',
        company_a,item_a,customer_a), '23514');
    INSERT INTO standby_item_issues(company_id,standby_item_id,customer_id,service_request_id,received_product_id,issued_date,due_date)
        VALUES (company_a,item_a,customer_a,request_a,product_a,'2026-09-16','2026-09-20')
        RETURNING id INTO issue_a;
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO standby_item_issues(company_id,standby_item_id,customer_id,issued_date) VALUES (%L,%L,%L,''2026-09-17'')',
        company_a,item_a,customer_a), '23505');
    UPDATE standby_item_issues SET returned_at=now() WHERE id=issue_a;
    INSERT INTO standby_item_issues(company_id,standby_item_id,customer_id,received_product_id,issued_date)
        VALUES (company_a,item_a,customer_a,product_a2,'2026-09-18');

    -- Archived configuration remains referenced and readable.
    UPDATE service_profiles SET is_active=false WHERE id=profile_a;
    UPDATE service_profile_statuses SET enabled=false WHERE id=status_a;
    IF NOT EXISTS (SELECT 1 FROM service_requests WHERE id=request_a AND status_id=status_a) THEN
        RAISE EXCEPTION 'Archiving configuration lost a service request';
    END IF;
END;
$$;

\ir ../migrations/000002_service_transactions.down.sql
DO $$ BEGIN
    IF (SELECT count(*) FROM information_schema.tables
        WHERE table_schema=current_schema() AND table_type='BASE TABLE') <> 13 THEN
        RAISE EXCEPTION 'T04 down migration did not leave the T03 schema intact';
    END IF;
END $$;
\ir ../migrations/000001_company_master_data.down.sql
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema()) THEN
        RAISE EXCEPTION 'Full down migration left tables behind';
    END IF;
END $$;

-- Verify the complete migration chain can be reapplied after rollback.
\ir ../migrations/000001_company_master_data.up.sql
\ir ../migrations/000002_service_transactions.up.sql
DO $$ BEGIN
    IF (SELECT count(*) FROM information_schema.tables
        WHERE table_schema=current_schema() AND table_type='BASE TABLE') <> 17 THEN
        RAISE EXCEPTION 'Migration reapply did not recreate all 17 tables';
    END IF;
END $$;
ROLLBACK;
\echo 'T04 migration, constraints, down chain and reapply checks passed; all test changes rolled back.'
