# Database migrations

T03 is implemented in `000001_company_master_data.up.sql`, T04 in `000002_service_transactions.up.sql`, T05/T06 authentication storage in `000003_authentication.up.sql`, T25 optional user sign-in phone in `000004_user_phone.up.sql`, T28 forced password change in `000005_must_change_password.up.sql`, T29 reset links in `000006_password_reset_tokens.up.sql`, T35/T36 history ordering and stand-by issue users in `000007_record_history_events.up.sql`, T33 mobile-number customers (duplicates merged, customer number dropped) in `000008_customer_mobile_identity.up.sql`, and the dropped company code in `000009_drop_company_code.up.sql`, each with a corresponding down migration. Together they create seventeen Phase 1 business tables plus two authentication tables. See the [master-schema decisions](../docs/schema-decisions.md), [transaction-schema decisions](../docs/transaction-schema-decisions.md), and [authentication decisions](../docs/authentication-decisions.md).

## Validate without applying the application schema

From `api/`, using a local PostgreSQL role with permission to create schemas in the verification database:

```sh
psql -X -w -d postgres -f tests/migration_000001_company_master_data.sql
psql -X -w -d postgres -f tests/migration_000002_service_transactions.sql
psql -X -w -d postgres -f tests/migration_000003_authentication.sql
psql -X -w -d postgres -f tests/migration_000004_user_phone.sql
psql -X -w -d postgres -f tests/migration_000005_must_change_password.sql
psql -X -w -d postgres -f tests/migration_000006_password_reset_tokens.sql
psql -X -w -d postgres -f tests/migration_000007_record_history_events.sql
psql -X -w -d postgres -f tests/migration_000008_customer_mobile_identity.sql
```

Each test uses a unique schema, verifies its tables, constraints and indexes, exercises down/reapply, and rolls back all changes. The T04 and T05 tests apply their prerequisite migrations first. These tests do not load `.env` or connect to the configured application database. PostgreSQL 14.20 verification passed.

## Apply migrations

No migrations run automatically at API startup. **Whenever you update the API code, apply pending migrations before restarting the API.** Otherwise the new code queries columns that do not exist yet and requests fail with HTTP 500 (for example `column u.phone does not exist` on login until 000004 is applied).

Use the migrate script from `api/`. It needs `psql` and the target database in `DATABASE_URL`, or the standard `PGHOST`/`PGPORT`/`PGDATABASE`/`PGUSER` variables:

```sh
export DATABASE_URL='postgres://USER:PASSWORD@HOST:5432/DBNAME?sslmode=disable'
scripts/migrate.sh status   # list applied and pending migrations
pg_dump "$DATABASE_URL" > backup-$(date +%F).sql   # back up first
scripts/migrate.sh up       # show what will run, confirm, then apply
```

Then restart the API.

- The script applies pending files in numeric order. Each file runs in its own transaction together with its row in `schema_migrations`, so a failed migration leaves nothing half-applied. Running it again only applies what is still pending.
- **Existing databases:** if `schema_migrations` does not exist yet (databases set up with the manual `psql` commands), the first run detects which migrations are already in place from the tables and columns, and records them without re-running them.
- **000008 (T33)** merges customers that share a mobile number into the oldest one and deletes the copies. The script prints how many will be merged and asks for confirmation. This cannot be undone, so back up first.
- `scripts/migrate.sh up --yes` skips the prompt, for deployment scripts.
- Down migrations are not run by the script. They are for development only (`psql -f migrations/NNNNNN_name.down.sql`, newest first) and drop data.

### Manual alternative

If you cannot use the script, apply each pending file once, in numeric order, each in a single transaction:

```sh
psql -X --set ON_ERROR_STOP=1 --single-transaction -f migrations/000004_user_phone.up.sql
```

Files fail on existing tables rather than silently masking schema differences. A database migrated by hand has no `schema_migrations` table; the script detects its state on first use.
