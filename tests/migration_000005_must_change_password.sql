\set ON_ERROR_STOP on
BEGIN;
SELECT 'serviceops_t28_' || replace(gen_random_uuid()::text, '-', '') AS test_schema \gset
CREATE SCHEMA :"test_schema";
SET LOCAL search_path TO :"test_schema", pg_catalog;
\ir ../migrations/000001_company_master_data.up.sql
\ir ../migrations/000002_service_transactions.up.sql
\ir ../migrations/000003_authentication.up.sql
\ir ../migrations/000004_user_phone.up.sql
\ir ../migrations/000005_must_change_password.up.sql

DO $$
DECLARE
    company_id uuid;
BEGIN
    INSERT INTO companies(name,code) VALUES ('Company','C') RETURNING id INTO company_id;
    INSERT INTO users(company_id,name,email,password_hash,role)
        VALUES (company_id,'Existing','existing@example.test','encoded','ADMIN');
    INSERT INTO users(company_id,name,email,password_hash,role,must_change_password)
        VALUES (company_id,'Invited','invited@example.test','encoded','USER',true);
    IF (SELECT must_change_password FROM users WHERE email='existing@example.test') THEN
        RAISE EXCEPTION 'Existing users must not be forced to change their password';
    END IF;
    BEGIN
        UPDATE users SET must_change_password=NULL WHERE email='invited@example.test';
        RAISE EXCEPTION 'NULL must_change_password unexpectedly succeeded';
    EXCEPTION WHEN not_null_violation THEN NULL;
    END;
END $$;

\ir ../migrations/000005_must_change_password.down.sql
\ir ../migrations/000005_must_change_password.up.sql
ROLLBACK;
\echo 'T28 must_change_password migration, default, not-null, down and reapply checks passed; all test changes rolled back.'
