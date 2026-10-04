# Company and master-data schema decisions

These choices implement T03 against the finalized [T01 requirements](t01-requirements.md). The application database has not been migrated.

## Tenant ownership and keys

- Every table uses a UUID `id` with `gen_random_uuid()` as its default. PostgreSQL 14 or later is the baseline; no extension is installed.
- `staff_role_assignments` adds `id` and `company_id` to the two documented references. Unique `(company_id, staff_id, role_id)` prevents duplicate assignments.
- Tenant-owned parents expose unique `(company_id, id)` keys. Composite foreign keys prevent relationships across companies. T06 must still authorize every API operation.
- SUPER_ADMIN users have no company; ADMIN and USER accounts require one.
- User emails must already be trimmed and lowercase and are globally case-insensitively unique. T05 normalizes them before persistence.
- Foreign keys use PostgreSQL's default NO ACTION behavior. No cascading business-data deletion is configured.

## Counters profiles and fields

- Each company starts `next_customer_number` at 1001. T08 will lock and increment it in the same transaction that creates `C-{number}`.
- Profiles start `next_number` at 1001. T15 will allocate `{prefix}{number}` inside the request transaction. Prefixes contain 1–10 uppercase ASCII letters and are unique within a company.
- `core_labels` stores exactly four nonempty labels for `service_date`, `customer_no`, `customer_name`, and `customer_contact`. The database checks keys, JSON value types and 200-character limits. These configure relational core controls and are separate from dynamic fields.
- Sent and received mappings are nullable so a profile can be created before its statuses. Composite foreign keys require each mapping to use the same company and profile.
- Profiles archive with `is_active=false`; fields and statuses disable with `enabled=false`. No historical definition is deleted.
- Dynamic field keys use stable lowercase snake-case identifiers up to 64 characters and are immutable through a trigger. The eight allowed field types match T01.
- Fields default to optional, enabled and non-system with order zero and empty JSON object configuration. Statuses default to enabled, non-initial and non-closed with order zero. T12/T13 enforce per-type configuration and workflow cardinality.

## Values and defaults

- Companies default to `ACTIVE` and allow `ACTIVE`/`SUSPENDED`.
- Users, staff, charges and shops default to `ACTIVE` and allow `ACTIVE`/`INACTIVE`.
- Products default to `ACTIVE` and also allow `DISCONTINUED`.
- Stand-by items default to `AVAILABLE` and allow `AVAILABLE`/`ISSUED`/`UNDER_MAINTENANCE`.
- Customer and staff contacts are required and nonempty. Text lengths follow the finalized API limits.
- Stand-by price is an optional nonnegative `numeric(12,2)` reference amount. It adds no billing behavior.
- Company timestamps use `timestamptz` with `now()` defaults. Future updates maintain `updated_at` explicitly.
- Company code, role name and serial number are not made unique because the finalized contract does not require it. Customer numbers and profile prefixes are tenant-unique; field keys are profile-unique; user email is globally unique.

## Onboarding boundary

The [HTML review](html-reference-review.md) defines Job Card and Refill defaults, including system roles, statuses and 24 dynamic fields. Those records are created per company by T07 onboarding in one transaction. A global migration cannot safely seed tenant-owned rows. T03 provides all required columns, defaults and constraints.

Authentication sessions/rate limits are technical tables for T05. Transaction tables and Product Received belong to T04. Notification functionality is deferred.

## Later changes (4 October 2026)

- **T33, migration 000008:** customers are identified by mobile number. A unique index on `(company_id, digits of contact)` allows one customer per mobile number per company. The migration first merges existing duplicates into the oldest customer (lowest former customer number): the kept customer takes any missing email/address, and records, stand-by issues and history references move to it before the others are deleted. `customers.customer_no`, `companies.next_customer_number` and the `customer_no` core label are dropped; core labels are now `service_date`, `customer_name` and `customer_contact`.
- **Migration 000009:** `companies.code` is dropped; companies are identified by their UUID.

## Verification

The SQL test covers all thirteen tables, UUID/tenant columns, primary keys, counters/defaults, exact core-label structure, normalized statuses/emails, required contacts, customer/prefix formats, lookup indexes, duplicate rejection, cross-tenant references, same-profile mappings, immutable keys, JSON configuration, archive retention and down/reapply execution.

The test runs in a unique schema inside a transaction and always rolls back. PostgreSQL 14.20 verification passed and left no test schemas. The application database remains unchanged.
