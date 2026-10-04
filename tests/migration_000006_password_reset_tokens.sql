\set ON_ERROR_STOP on
BEGIN;
SELECT 'serviceops_t29_' || replace(gen_random_uuid()::text, '-', '') AS test_schema \gset
CREATE SCHEMA :"test_schema";
SET LOCAL search_path TO :"test_schema", pg_catalog;
\ir ../migrations/000001_company_master_data.up.sql
\ir ../migrations/000002_service_transactions.up.sql
\ir ../migrations/000003_authentication.up.sql
\ir ../migrations/000004_user_phone.up.sql
\ir ../migrations/000005_must_change_password.up.sql
\ir ../migrations/000006_password_reset_tokens.up.sql

DO $$
DECLARE
    company_id uuid;
    user_id uuid;
BEGIN
    INSERT INTO companies(name,code) VALUES ('Company','C') RETURNING id INTO company_id;
    INSERT INTO users(company_id,name,email,password_hash,role)
        VALUES (company_id,'User','user@example.test','encoded','USER') RETURNING id INTO user_id;
    INSERT INTO password_reset_tokens(user_id,token_hash,expires_at)
        VALUES (user_id,decode(repeat('ab',32),'hex'),now()+interval '30 minutes');
    BEGIN
        INSERT INTO password_reset_tokens(user_id,token_hash,expires_at)
            VALUES (user_id,decode(repeat('ab',32),'hex'),now()+interval '30 minutes');
        RAISE EXCEPTION 'Duplicate token hash unexpectedly succeeded';
    EXCEPTION WHEN unique_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO password_reset_tokens(user_id,token_hash,expires_at)
            VALUES (user_id,decode('abcd','hex'),now()+interval '30 minutes');
        RAISE EXCEPTION 'Short token hash unexpectedly succeeded';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO password_reset_tokens(user_id,token_hash,expires_at)
            VALUES (user_id,decode(repeat('cd',32),'hex'),now()-interval '1 second');
        RAISE EXCEPTION 'Expired-at-creation token unexpectedly succeeded';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    DELETE FROM users WHERE id=user_id;
    IF EXISTS (SELECT 1 FROM password_reset_tokens) THEN
        RAISE EXCEPTION 'Tokens must be removed with their user';
    END IF;
END $$;

\ir ../migrations/000006_password_reset_tokens.down.sql
\ir ../migrations/000006_password_reset_tokens.up.sql
ROLLBACK;
\echo 'T29 password reset token migration, constraints, cascade, down and reapply checks passed; all test changes rolled back.'
