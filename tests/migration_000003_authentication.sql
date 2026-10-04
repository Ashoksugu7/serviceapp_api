\set ON_ERROR_STOP on
BEGIN;
SELECT 'serviceops_t05_' || replace(gen_random_uuid()::text, '-', '') AS test_schema \gset
CREATE SCHEMA :"test_schema";
SET LOCAL search_path TO :"test_schema", pg_catalog;
\ir ../migrations/000001_company_master_data.up.sql
\ir ../migrations/000002_service_transactions.up.sql
\ir ../migrations/000003_authentication.up.sql

DO $$
DECLARE
    company_id uuid;
    user_id uuid;
    session_id uuid;
BEGIN
    IF (SELECT count(*) FROM information_schema.tables
        WHERE table_schema=current_schema() AND table_type='BASE TABLE') <> 19 THEN
        RAISE EXCEPTION 'Expected 19 tables after T05';
    END IF;
    IF (SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema()
        AND indexname IN ('auth_sessions_user_active_idx','auth_sessions_expiry_idx','auth_rate_limits_expiry_idx')) <> 3 THEN
        RAISE EXCEPTION 'Required authentication indexes are missing';
    END IF;

    INSERT INTO companies(name,code) VALUES ('Company','C') RETURNING id INTO company_id;
    INSERT INTO users(company_id,name,email,password_hash,role)
        VALUES (company_id,'Admin','admin@example.test','encoded','ADMIN') RETURNING id INTO user_id;
    INSERT INTO auth_sessions(user_id,expires_at)
        VALUES (user_id,now()+interval '30 minutes') RETURNING id INTO session_id;
    UPDATE auth_sessions SET revoked_at=now() WHERE id=session_id;

    BEGIN
        INSERT INTO auth_sessions(user_id,expires_at) VALUES (user_id,now()-interval '1 second');
        RAISE EXCEPTION 'Expired-at-creation session unexpectedly succeeded';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO auth_rate_limits(scope,key_hash,window_start,attempts,expires_at)
            VALUES ('EMAIL',decode(repeat('00',32),'hex'),now(),-1,now()+interval '15 minutes');
        RAISE EXCEPTION 'Negative attempts unexpectedly succeeded';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO auth_rate_limits(scope,key_hash,window_start,expires_at)
            VALUES ('RAW_EMAIL',decode(repeat('00',32),'hex'),now(),now()+interval '15 minutes');
        RAISE EXCEPTION 'Raw email scope unexpectedly succeeded';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    INSERT INTO auth_rate_limits(scope,key_hash,window_start,attempts,expires_at)
        VALUES ('EMAIL',decode(repeat('01',32),'hex'),date_trunc('minute',now()),1,now()+interval '15 minutes')
        ON CONFLICT (scope,key_hash,window_start)
        DO UPDATE SET attempts=auth_rate_limits.attempts+1;
END $$;

\ir ../migrations/000003_authentication.down.sql
DO $$ BEGIN
    IF (SELECT count(*) FROM information_schema.tables
        WHERE table_schema=current_schema() AND table_type='BASE TABLE') <> 17 THEN
        RAISE EXCEPTION 'T05 down migration did not leave T04 intact';
    END IF;
END $$;
\ir ../migrations/000003_authentication.up.sql
DO $$ BEGIN
    IF (SELECT count(*) FROM information_schema.tables
        WHERE table_schema=current_schema() AND table_type='BASE TABLE') <> 19 THEN
        RAISE EXCEPTION 'T05 migration reapply failed';
    END IF;
END $$;
ROLLBACK;
\echo 'T05 authentication migration, constraints, down and reapply checks passed; all test changes rolled back.'
