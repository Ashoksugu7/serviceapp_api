\set ON_ERROR_STOP on
BEGIN;
SELECT 'serviceops_t25_' || replace(gen_random_uuid()::text, '-', '') AS test_schema \gset
CREATE SCHEMA :"test_schema";
SET LOCAL search_path TO :"test_schema", pg_catalog;
\ir ../migrations/000001_company_master_data.up.sql
\ir ../migrations/000002_service_transactions.up.sql
\ir ../migrations/000003_authentication.up.sql
\ir ../migrations/000004_user_phone.up.sql

DO $$
DECLARE
    company_id uuid;
BEGIN
    INSERT INTO companies(name,code) VALUES ('Company','C') RETURNING id INTO company_id;
    INSERT INTO users(company_id,name,email,password_hash,role,phone)
        VALUES (company_id,'Admin','admin@example.test','encoded','ADMIN','9876543210');
    INSERT INTO users(company_id,name,email,password_hash,role)
        VALUES (company_id,'No phone','nophone@example.test','encoded','USER');
    INSERT INTO users(company_id,name,email,password_hash,role)
        VALUES (company_id,'No phone 2','nophone2@example.test','encoded','USER');
    BEGIN
        INSERT INTO users(company_id,name,email,password_hash,role,phone)
            VALUES (company_id,'Duplicate','dup@example.test','encoded','USER','9876543210');
        RAISE EXCEPTION 'Duplicate phone unexpectedly succeeded';
    EXCEPTION WHEN unique_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO users(company_id,name,email,password_hash,role,phone)
            VALUES (company_id,'Formatted','fmt@example.test','encoded','USER','98765 43210');
        RAISE EXCEPTION 'Non-digit phone unexpectedly succeeded';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
END $$;

\ir ../migrations/000004_user_phone.down.sql
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
        WHERE table_schema=current_schema() AND table_name='users' AND column_name='phone') THEN
        RAISE EXCEPTION 'T25 down migration did not remove users.phone';
    END IF;
END $$;
\ir ../migrations/000004_user_phone.up.sql
ROLLBACK;
\echo 'T25 user phone migration, uniqueness, format, down and reapply checks passed; all test changes rolled back.'
