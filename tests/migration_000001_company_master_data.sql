\set ON_ERROR_STOP on
BEGIN;
-- All objects live in a unique schema inside this transaction. No public or
-- application tables are read or changed; disconnect/error also rolls back.
SELECT 'serviceops_t03_' || replace(gen_random_uuid()::text, '-', '') AS test_schema \gset
CREATE SCHEMA :"test_schema";
SET LOCAL search_path TO :"test_schema", pg_catalog;
\ir ../migrations/000001_company_master_data.up.sql

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
    a uuid; b uuid; pa uuid; pb uuid; pa2 uuid; sa uuid; sb uuid;
    employee uuid; local_role uuid; foreign_role uuid; field uuid;
    count_tables integer;
BEGIN
    SELECT count(*) INTO count_tables FROM information_schema.tables
    WHERE table_schema = current_schema() AND table_type = 'BASE TABLE';
    IF count_tables <> 13 THEN RAISE EXCEPTION 'Expected 13 tables, got %', count_tables; END IF;
    IF (SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND column_name='id' AND data_type='uuid' AND is_nullable='NO') <> 13 THEN
        RAISE EXCEPTION 'Every table must have a non-null UUID id';
    END IF;
    IF (SELECT count(*) FROM information_schema.table_constraints WHERE table_schema=current_schema() AND constraint_type='PRIMARY KEY') <> 13 THEN
        RAISE EXCEPTION 'Every table must have a primary key';
    END IF;
    IF (SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND column_name='company_id' AND data_type='uuid' AND is_nullable='NO') <> 11 THEN
        RAISE EXCEPTION 'Business tables must have non-null tenant UUIDs';
    END IF;
    IF (SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname IN ('customers_company_name_idx','customers_company_contact_idx')) <> 2 THEN
        RAISE EXCEPTION 'Missing customer lookup indexes';
    END IF;

    INSERT INTO companies(name, code) VALUES ('A', 'A') RETURNING id INTO a;
    INSERT INTO companies(name, code) VALUES ('B', 'B') RETURNING id INTO b;
    IF EXISTS (SELECT 1 FROM companies WHERE status <> 'ACTIVE' OR next_customer_number <> 1001) THEN
        RAISE EXCEPTION 'Company defaults do not match the contract';
    END IF;
    PERFORM pg_temp.expect_error('INSERT INTO companies(name,code,status) VALUES (''Bad'',''BAD'',''active'')', '23514');
    INSERT INTO users(name, email, password_hash, role, status)
        VALUES ('Platform', 'root@example.test', 'test-hash', 'SUPER_ADMIN', 'ACTIVE');
    INSERT INTO users(company_id, name, email, password_hash, role, status)
        VALUES (a, 'Admin', 'admin@example.test', 'test-hash', 'ADMIN', 'ACTIVE');
    PERFORM pg_temp.expect_error(format('INSERT INTO users(company_id,name,email,password_hash,role,status) VALUES (%L,''Duplicate'',''admin@example.test'',''hash'',''USER'',''ACTIVE'')', b), '23505');
    PERFORM pg_temp.expect_error(format('INSERT INTO users(company_id,name,email,password_hash,role,status) VALUES (%L,''Case duplicate'',''Admin@Example.Test'',''hash'',''USER'',''ACTIVE'')', b), '23514');
    PERFORM pg_temp.expect_error('INSERT INTO users(name,email,password_hash,role,status) VALUES (''No tenant'',''missing@example.test'',''hash'',''USER'',''ACTIVE'')', '23514');
    PERFORM pg_temp.expect_error(format('INSERT INTO users(company_id,name,email,password_hash,role,status) VALUES (%L,''Platform'',''invalid@example.test'',''hash'',''SUPER_ADMIN'',''ACTIVE'')', a), '23514');

    INSERT INTO customers(company_id, customer_no, name, contact) VALUES (a, 'C-1001', 'Customer A', '111'), (b, 'C-1001', 'Customer B', '222');
    PERFORM pg_temp.expect_error(format('INSERT INTO customers(company_id,customer_no,name,contact) VALUES (%L,''C-1001'',''Duplicate'',''333'')', a), '23505');
    PERFORM pg_temp.expect_error(format('INSERT INTO customers(company_id,customer_no,name,contact) VALUES (%L,''C1'',''Invalid'',''333'')', a), '23514');
    PERFORM pg_temp.expect_error(format('INSERT INTO customers(company_id,customer_no,name) VALUES (%L,''C-1002'',''No contact'')', a), '23502');
    INSERT INTO staff(company_id,name,contact) VALUES (a,'Engineer','111') RETURNING id INTO employee;
    INSERT INTO staff_roles(company_id,name) VALUES (a,'Engineer') RETURNING id INTO local_role;
    INSERT INTO staff_roles(company_id,name) VALUES (b,'Engineer') RETURNING id INTO foreign_role;
    INSERT INTO staff_role_assignments(company_id,staff_id,role_id) VALUES (a,employee,local_role);
    PERFORM pg_temp.expect_error(format('INSERT INTO staff_role_assignments(company_id,staff_id,role_id) VALUES (%L,%L,%L)', a,employee,local_role), '23505');
    PERFORM pg_temp.expect_error(format('INSERT INTO staff_role_assignments(company_id,staff_id,role_id) VALUES (%L,%L,%L)', a,employee,foreign_role), '23503');
    PERFORM pg_temp.expect_error(format('DELETE FROM staff_roles WHERE id=%L', local_role), '23503');

    INSERT INTO service_profiles(company_id,name,prefix) VALUES (a,'Repair','PC') RETURNING id INTO pa;
    INSERT INTO service_profiles(company_id,name,prefix) VALUES (b,'Repair','PC') RETURNING id INTO pb;
    INSERT INTO service_profiles(company_id,name,prefix) VALUES (a,'Mobile','MB') RETURNING id INTO pa2;
    IF EXISTS (SELECT 1 FROM service_profiles WHERE next_number <> 1001) THEN
        RAISE EXCEPTION 'Profile counter default does not match the contract';
    END IF;
    IF EXISTS (
        SELECT 1 FROM service_profiles
        WHERE core_labels <> '{"service_date":"Date","customer_no":"Customer No","customer_name":"Customer Name","customer_contact":"Contact No"}'::jsonb
    ) THEN RAISE EXCEPTION 'Core label defaults do not match the contract'; END IF;
    PERFORM pg_temp.expect_error(format('INSERT INTO service_profiles(company_id,name,prefix) VALUES (%L,''Invalid'',''P-1'')', a), '23514');
    PERFORM pg_temp.expect_error(format('UPDATE service_profiles SET core_labels=''{"service_date":"Date"}''::jsonb WHERE id=%L', pa), '23514');
    UPDATE service_profiles SET core_labels=jsonb_set(core_labels, '{customer_no}', '"Customer ID"') WHERE id=pa;
    PERFORM pg_temp.expect_error(format('INSERT INTO service_profiles(company_id,name,prefix) VALUES (%L,''Duplicate'',''PC'')', a), '23505');
    INSERT INTO service_profile_statuses(company_id,profile_id,name) VALUES (a,pa,'Sent') RETURNING id INTO sa;
    INSERT INTO service_profile_statuses(company_id,profile_id,name) VALUES (b,pb,'Sent') RETURNING id INTO sb;
    UPDATE service_profiles SET sent_status_id=sa, received_status_id=sa WHERE id=pa;
    PERFORM pg_temp.expect_error(format('UPDATE service_profiles SET sent_status_id=%L WHERE id=%L', sb,pa), '23503');
    PERFORM pg_temp.expect_error(format('UPDATE service_profiles SET received_status_id=%L WHERE id=%L', sa,pa2), '23503');
    PERFORM pg_temp.expect_error(format('DELETE FROM service_profile_statuses WHERE id=%L', sa), '23503');

    INSERT INTO service_profile_fields(company_id,profile_id,field_key,label,field_type)
        VALUES (a,pa,'complaint','Complaint','text') RETURNING id INTO field;
    UPDATE service_profile_fields SET label='Problem', enabled=false WHERE id=field;
    IF NOT EXISTS (SELECT 1 FROM service_profile_fields WHERE id=field AND field_key='complaint' AND label='Problem' AND NOT enabled) THEN RAISE EXCEPTION 'Relabel/disable failed'; END IF;
    PERFORM pg_temp.expect_error(format('UPDATE service_profile_fields SET field_key=''changed'' WHERE id=%L',field), '23514');
    PERFORM pg_temp.expect_error(format('UPDATE service_profile_fields SET config=''[]''::jsonb WHERE id=%L',field), '23514');
    PERFORM pg_temp.expect_error(format('UPDATE service_profile_fields SET field_type=''unknown'' WHERE id=%L',field), '23514');
    PERFORM pg_temp.expect_error(format('INSERT INTO service_profile_fields(company_id,profile_id,field_key,label,field_type) VALUES (%L,%L,''complaint'',''Duplicate'',''text'')',a,pa), '23505');
    PERFORM pg_temp.expect_error(format('INSERT INTO service_profile_statuses(company_id,profile_id,name) VALUES (%L,%L,''Foreign'')',a,pb), '23503');

    INSERT INTO products(company_id,profile_id,name) VALUES (a,pa,'Laptop');
    INSERT INTO charges(company_id,profile_id,name) VALUES (a,pa,'Repair');
    INSERT INTO out_store_shops(company_id,shop_name) VALUES (a,'Company shop');
    INSERT INTO out_store_shops(company_id,profile_id,shop_name) VALUES (a,pa,'Profile shop');
    PERFORM pg_temp.expect_error(format('INSERT INTO products(company_id,profile_id,name) VALUES (%L,%L,''Foreign'')',a,pb), '23503');
    PERFORM pg_temp.expect_error(format('INSERT INTO charges(company_id,profile_id,name) VALUES (%L,%L,''Foreign'')',a,pb), '23503');
    PERFORM pg_temp.expect_error(format('INSERT INTO out_store_shops(company_id,profile_id,shop_name) VALUES (%L,%L,''Foreign'')',a,pb), '23503');
    PERFORM pg_temp.expect_error(format('INSERT INTO products(company_id,profile_id,name,status) VALUES (%L,%L,''Bad'',''INACTIVE'')',a,pa), '23514');
    INSERT INTO standby_items(company_id,name,price) VALUES (a,'Loaner',100);
    PERFORM pg_temp.expect_error(format('INSERT INTO standby_items(company_id,name,price,status) VALUES (%L,''Invalid'',-1,''AVAILABLE'')',a), '23514');
    PERFORM pg_temp.expect_error(format('INSERT INTO standby_items(company_id,name,status) VALUES (%L,''Invalid status'',''INACTIVE'')',a), '23514');
    UPDATE service_profiles SET is_active=false WHERE id=pa;
    IF NOT EXISTS (SELECT 1 FROM service_profile_fields WHERE id=field) THEN RAISE EXCEPTION 'Archive lost fields'; END IF;
END;
$$;

\ir ../migrations/000001_company_master_data.down.sql
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema()) THEN
        RAISE EXCEPTION 'Down migration left tables behind';
    END IF;
END $$;
-- Verify the same migration can be applied again after rollback.
\ir ../migrations/000001_company_master_data.up.sql
ROLLBACK;
\echo 'T03 migration, constraint, down and reapply checks passed; all test changes rolled back.'
