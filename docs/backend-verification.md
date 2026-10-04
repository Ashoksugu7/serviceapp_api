# Backend verification

T23 verifies the Phase 1 API implementation against the approved requirements, OpenAPI contract and PostgreSQL migrations. Verification ran on 16 September 2026 against PostgreSQL 14.20 using disposable schemas. The configured application schema was not migrated.

## Results

| Area | Verified behavior |
| --- | --- |
| Authorization | ADMIN, USER and SUPER_ADMIN route boundaries; USER master reads and transaction writes; SUPER_ADMIN administration-only scope; inactive identity rejection; company path isolation. |
| Tenant isolation | Foreign company IDs are rejected in paths, request bodies, query filters and dynamic product references. Repository relationships and composite foreign keys enforce the same boundary. |
| Schema | All 17 business tables, primary keys, tenant columns, required indexes, constraints, triggers, migration order, down chain and reapply behavior. The two authentication tables are verified separately. |
| Numbering | Concurrent request creation produces unique numbers. Job Card and Refill counters are independent, each company starts its own sequence, and failed inline dispatch rolls back the request and counter. |
| Dynamic forms | Required, scalar, multiple-choice, toggle-date and tenant/profile reference validation; immutable/reserved keys; historical type protection; field/status disable rules; partial core-label merge; formula graph, read-only calculated fields, decimal evaluation and six-place half-away rounding. |
| History and configuration | Invalid edits do not write history. Actual core, dynamic and status changes do. Current labels resolve for retained historical values after a product or field is disabled. Initial status replacement and mapped-status protection are enforced. |
| Workflows | Closed-record rules, inline and standalone Out-Store dispatch, mapped statuses, receive-back retries, re-dispatch, stand-by issue/return, concurrent issue rejection and old-return retry safety. |
| HTTP contract | All 64 OpenAPI operations are registered. Representative requests and responses, authorization metadata, 38 positive/negative schemas and the shared JSON error envelope pass. |

The audit corrected configuration behavior found during verification: profile DELETE routing, initial and mapped status safeguards, Out-Store mapping enablement rules, partial core-label updates, canonical JWT encoding checks, field configuration validation, historical type protection, and server-side numeric formulas. No remaining Phase 1 backend contract gap was found. UI implementation and T24 handover documentation remain separate work.

## Reproduce

Run from `api/` with a disposable PostgreSQL database URL:

```sh
SERVICEOPS_TEST_DATABASE_URL='postgres:///postgres?host=/tmp' go test ./...
SERVICEOPS_TEST_DATABASE_URL='postgres:///postgres?host=/tmp' go test -race ./...
go vet ./...
go mod tidy -diff
test -z "$(gofmt -l .)"
```

Validate the contract from the repository root using the environment described in `api/contracts/README.md`:

```sh
/tmp/serviceops-contract-validation/bin/python api/tests/validate_contract.py
```

Run the isolated SQL checks from `api/`:

```sh
psql -X -w -d postgres -f tests/migration_000001_company_master_data.sql
psql -X -w -d postgres -f tests/migration_000002_service_transactions.sql
psql -X -w -d postgres -f tests/migration_000003_authentication.sql
```

All test code is stored in `tests/`. Each SQL script creates its schema inside a transaction and rolls it back. Database-backed Go tests create uniquely named schemas and drop them after completion.
