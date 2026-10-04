\set ON_ERROR_STOP on
BEGIN;
SELECT 'serviceops_t35_' || replace(gen_random_uuid()::text, '-', '') AS test_schema \gset
CREATE SCHEMA :"test_schema";
SET LOCAL search_path TO :"test_schema", pg_catalog;
\ir ../migrations/000001_company_master_data.up.sql
\ir ../migrations/000002_service_transactions.up.sql
\ir ../migrations/000003_authentication.up.sql
\ir ../migrations/000004_user_phone.up.sql
\ir ../migrations/000005_must_change_password.up.sql
\ir ../migrations/000006_password_reset_tokens.up.sql
\ir ../migrations/000007_record_history_events.up.sql

DO $$
DECLARE
    company_id uuid;
    other_company uuid;
    user_id uuid;
    outsider uuid;
    customer_id uuid;
    item_id uuid;
BEGIN
    INSERT INTO companies(name,code) VALUES ('Company','C') RETURNING id INTO company_id;
    INSERT INTO companies(name,code) VALUES ('Other','O') RETURNING id INTO other_company;
    INSERT INTO users(company_id,name,email,password_hash,role)
        VALUES (company_id,'User','user@example.test','encoded','USER') RETURNING id INTO user_id;
    INSERT INTO users(company_id,name,email,password_hash,role)
        VALUES (other_company,'Outsider','outsider@example.test','encoded','USER') RETURNING id INTO outsider;
    INSERT INTO customers(company_id,customer_no,name,contact)
        VALUES (company_id,'C-1','Customer','9876543210') RETURNING id INTO customer_id;
    INSERT INTO standby_items(company_id,name) VALUES (company_id,'Loaner') RETURNING id INTO item_id;

    INSERT INTO standby_item_issues(company_id,standby_item_id,customer_id,issued_date)
        VALUES (company_id,item_id,customer_id,'2026-10-01');
    UPDATE standby_item_issues SET returned_at=now() WHERE standby_item_id=item_id;
    INSERT INTO standby_item_issues(company_id,standby_item_id,customer_id,issued_date,issued_by)
        VALUES (company_id,item_id,customer_id,'2026-10-02',user_id);
    UPDATE standby_item_issues SET returned_at=now(),returned_by=user_id WHERE issued_by=user_id;
    BEGIN
        INSERT INTO standby_item_issues(company_id,standby_item_id,customer_id,issued_date,issued_by)
            VALUES (company_id,item_id,customer_id,'2026-10-03',outsider);
        RAISE EXCEPTION 'Issuer from another company unexpectedly succeeded';
    EXCEPTION WHEN foreign_key_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO standby_item_issues(company_id,standby_item_id,customer_id,issued_date,returned_by)
            VALUES (company_id,item_id,customer_id,'2026-10-03',user_id);
        RAISE EXCEPTION 'Returned-by without a return unexpectedly succeeded';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    IF (SELECT column_default FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='service_request_history' AND column_name='created_at') <> 'clock_timestamp()' THEN
        RAISE EXCEPTION 'History rows must default to clock_timestamp()';
    END IF;
END $$;

\ir ../migrations/000007_record_history_events.down.sql
\ir ../migrations/000007_record_history_events.up.sql
ROLLBACK;
\echo 'T35/T36 history ordering and stand-by issue user migration, tenant foreign keys, return check, down and reapply checks passed; all test changes rolled back.'
