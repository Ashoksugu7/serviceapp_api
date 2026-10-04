# T01 requirements and decisions

**Status: Complete as an implementation baseline.** The v3 documents, supplied HTML, confirmed user choices and technical defaults below define Phase 1. Completing T01 means requirements are recorded; it does not mean these endpoints or schema changes are implemented.

## Sources and authority

- [High-Level Design](../../ServiceOps360_HLD_v3.docx), [Database Schema](../../ServiceOps360_Database_Schema_v3.docx), [API Specification](../../ServiceOps360_API_Specification_v3.docx).
- [HTML source review](html-reference-review.md) records observed behavior and prototype limitations. Browser/visual parity remains UI verification work.
- [OpenAPI contract](../contracts/openapi.json) defines request/response schemas, route access, field limits, query parameters and errors. [Readable API contract](api-contract-draft.md) summarizes it.
- Explicit user choices below are confirmed. Other rules are selected technical defaults to make the implementation concrete; they are not presented as user statements or facts from the prototype. Future changes should update the contract and decision record together.

## Confirmed scope

| Decision | Confirmed choice |
| --- | --- |
| Company USER | Read masters/forms; manage service transactions. No master/configuration/user writes. |
| SUPER_ADMIN | Company, user and profile/form administration only; no business masters or service transactions. |
| Notifications | Defer templates, triggers and Notification Log. No notification delivery integration. |
| Amount fields | Retain Advance Amount, Total Amount and Balance Due as informational dynamic fields/formulas; no billing/payments. |
| Stand-by Product Received | Include a customer-device product selection on issue records. |
| Inline Out-Store | Allow Service Entry to create the request and its Out-Store dispatch atomically. |
| Request execution | Synchronous handlers; commit related database changes before returning success. |

> **Superseded 4 October 2026 (T32, T33):** customer numbers and the `customer_no` core key were removed — customers are found and de-duplicated by mobile number; company codes were removed; unused statuses can be deleted. See [schema decisions](schema-decisions.md#later-changes-4-october-2026) and [management and forms](management-and-forms.md).

## D01 Reference defaults

Use the [reference inventory](html-reference-review.md) for four protected core controls and all 24 configurable dynamic fields. Seed Job Card (prefix A, Out-Store enabled) and Refill (prefix RF, Out-Store disabled), with the listed statuses/mappings. Seed Attended By and Service Engineer as system staff roles; Delivered By is an initial custom role. Do not seed sample people or business transactions.

New custom profiles start with four core controls, no dynamic fields, the reference custom status list, Out-Store disabled and counter 1001. Mark Pending initial, or Received for Refill. Mark Delivered and Returned Not Repaired closed; Completed remains open so receipt/completion can precede delivery. These closed flags are technical workflow defaults because the HTML supplies labels, not flags.

Core keys are `service_date`, `customer_no`, `customer_name`, `customer_contact`. Their labels live in `service_profiles.core_labels` JSONB, a required schema addition. Date maps to the relational service_date; Customer No selects customer_id; name/contact are read-only projections of that customer. They are not writable dynamic fields. Profile PATCH merges supplied core_labels; controls cannot be removed or retyped. Reserve these four keys from dynamic field creation. Form responses always include the four core controls separately from dynamic fields.

## D02 and D03 Access

| Operation | SUPER_ADMIN | ADMIN | USER |
| --- | --- | --- | --- |
| Login / own identity / logout | Yes | Yes | Yes |
| Company list / create / suspend / reactivate | Yes | No | No |
| Company profile read / contact edit | Any company | Own company | Identity response only |
| Company user management | Any company | Own company | No |
| Customers, staff/roles, products, charges, shops, stand-by masters | No | Read/write | Read only |
| Profile/form read | Any company | Own company | Own company |
| Profile/field/status/mapping writes | Any company | Own company | No |
| Requests, history, Out-Store, issue/return | No | Own company | Own company |

Every company path and linked record is authorized against identity. Reject company scope mismatch with 403; resource IDs missing within an authorized scope yield 404. Never trust company_id or actor IDs in bodies. Transaction authors must be company users. SUPER_ADMIN can use profile configuration to select a staff role: expose role ID/name choices in form metadata only, without granting business-master endpoints or staff/customer data access.

## D04 Authentication and account lifecycle

- Company status is ACTIVE/SUSPENDED; user status ACTIVE/INACTIVE. Only SUPER_ADMIN may suspend/reactivate companies. ADMIN cannot change company status or create SUPER_ADMIN users. Prevent demoting/deactivating the last active company ADMIN using a transaction and company-row lock.
- Login returns a 30-minute JWT, no refresh token. Pin HS256, require a secret with at least 32 random bytes, and verify signature/issuer (`serviceops360`)/audience (`serviceops360-api`)/subject/expiry/token ID. Permit at most 30 seconds clock skew. Store signing secrets in environment configuration.
- Persist `auth_sessions` with UUID id/token ID, user_id, created_at, expires_at and nullable revoked_at. JWT jti references this row. Each request verifies the session plus the current user role/status and company status. Token company/role claims must agree with current identity; stale claims require login again. Never persist the raw bearer token.
- Logout revokes the presented session. Password changes revoke all sessions for that user in the same transaction. Login issuance and password changes lock the user row to prevent a concurrent old-password login surviving a reset. Deactivation immediately rejects all sessions; reactivation must not revive sessions revoked during deactivation.
- Use Argon2id with 19 MiB memory, 2 iterations, parallelism 1, random 16-byte salt and 32-byte hash, stored in a versioned encoded string. New passwords (changed by T26, 4 October 2026, at the user's request): 10–128 characters with at least one letter and one number, no trimming, allow spaces and Unicode, reject known compromised/common passwords via a packaged blocklist. No external password-check service is required. These parameters follow [OWASP password storage guidance](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html); password validation follows [OWASP authentication guidance](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html).
- Unknown email/bad password/inactive account login returns the same 401 message. Perform a dummy password verification for unknown accounts. Authenticated company suspension returns 403 COMPANY_SUSPENDED. Rate-limit failed login attempts to 5 per normalized email and 20 per client IP per 15 minutes; return 429 with Retry-After. Persist shared windows in `auth_rate_limits`, using HMAC-derived keys rather than raw email addresses. Do not trust forwarded IP headers unless the proxy is configured as trusted. Limits are tunable runtime defaults.
- Normalize login/account emails by trimming and lowercasing. Enforce case-insensitive global uniqueness with a reviewed index migration, after checking existing duplicates. Do not normalize passwords.
- Bootstrap SUPER_ADMIN through a local interactive CLI; no public route/default password. A transaction-scoped lock prevents concurrent bootstrap duplicates. Refuse when a SUPER_ADMIN already exists. Onboard company, first ADMIN, counters and profile defaults in one transaction.

## D05 HTTP contracts and numbering

Use [OpenAPI](../contracts/openapi.json) as the schema source. Pagination defaults to page 1/page_size 25, maximum 100; sort allowlists and UUID tie-breakers make ordering stable. Date filters are inclusive, and date_from cannot exceed date_to. Request search covers number, customer name/number/contact and string representations of submitted dynamic values within the authorized scope.

Bodies are limited to 1 MiB. Names/labels have a 200-character limit, email 254, contact 50, free text 4000, field keys 64, profiles 200 dynamic fields/100 statuses, choices 100 options. Reject unknown properties and blank required text. PATCH requires at least one property; omission preserves existing data, explicit null clears only nullable values. Runtime profile validation supplements OpenAPI's dynamic-value union.

Customer numbers are generated per company as C-1001, C-1002, and so on. Request numbers are prefix plus counter, e.g. A1001/RF1001; prefixes are unique uppercase A–Z strings of 1–10 characters. Customer creation increments companies.next_customer_number inside the insert transaction; request creation increments service_profiles.next_number inside its transaction. Never allocate using MAX/count. Prefix is immutable once any request exists; counters never reset or get reused. Limits are the OpenAPI integer range and database uniqueness constraints.

Do not fabricate GET routes for resources whose source only exposes list/PATCH. Resource-creation responses contain the resource; no universal Location header is promised. Password/hash fields never occur in response schemas.

## D06 and D07 Staff roles

Keep T03's UUID assignment id, required company_id, same-tenant staff/role foreign keys and unique `(company_id, staff_id, role_id)`. Staff create/PATCH supports role_ids: omitted means preserve, [] clears, provided arrays replace atomically after validating distinct company role IDs. Delete rejects system roles, assigned roles or roles referenced in field configuration with 409 RESOURCE_IN_USE. Readable staff includes role_ids.

## D08 Archive and historical preservation

Profile DELETE archives with is_active=false; field/status DELETE disables with enabled=false. Retain definitions and old values. Reject archiving the last active company profile. Reset UI behavior must use the existing permitted label/disable operations; it must not hard-delete historical configuration.

Archived profiles cannot create new requests; existing requests remain readable and editable under normal record-state rules. Disabled dynamic values display read-only and remain stored. Dynamic PATCH merges supplied keys: omitted values remain; null clears only optional enabled fields. Reject unknown/disabled keys. Formula values are recomputed; clients cannot override them.

Non-system field types can change only before a profile has requests. System controls cannot be disabled/deleted/retyped. API keys are immutable. Reads resolve current labels; the source schema does not provide historical label snapshots. Request detail returns customer and linked_values, with current label/selectable state, so historical inactive references remain readable without new selection privileges.

## D09 Field configuration and formulas

| Type | Value | Configuration |
| --- | --- | --- |
| text | String | multiline, format plain/phone/email, optional max_length up to 4000 |
| number | Finite JSON number | currency, optional formula `{a, op, b}` |
| date | ISO date or `{on, date}` | quick_pick or toggle_based, mutually exclusive |
| choice | String or distinct string array | options, multiple, buttons; buttons only for single choice |
| checkbox | Boolean | Required means supplied; false is valid |
| linked_product | UUID string | Active same-company/profile product |
| linked_charges | UUID array | Active same-company/profile charges |
| staff_role | UUID string | Active staff with configured company role_id |

Persist per-type configuration in JSONB using the names above. Arrays of linked UUIDs preserve selection order. Required multiple choices/charges cannot be empty. Optional toggle date on=false uses date=null; on=true requires a valid date. Quick picks use the user's local calendar date plus 0/3/7/14 days, without UTC conversion of the selected date.

Formulas use two same-profile numeric field keys and operators +, -, *, /. Normalize imported multiply/divide symbols; no executable expressions. Reject self-reference, cycles and more than 20 nested formula dependencies. Evaluate dependency order on the server. Missing optional numeric operands evaluate to zero; missing required operands fail validation. Division by zero fails validation. Use decimal arithmetic, round computed values half away from zero to six fractional digits before persistence, and render currency with two decimals. Numeric magnitudes are limited to 9,999,999,999.99; currency input is nonnegative, but a calculated balance may be negative. Reject overflow/non-finite values. These explicit rules correct the prototype's silent-zero division and undefined formula chaining.

Require exactly one enabled initial non-closed status when a profile accepts requests. Setting initial=true clears the previous initial atomically. Enabled initial status cannot be disabled until replaced. Mapped status cannot be disabled until mapping changes. Preserve references from existing requests to disabled statuses; allow transition to a valid enabled status.

## D10 Service requests and Out-Store

Company/profile/number/creator are immutable. Customer can change only before any Out-Store or linked stand-by history exists. USER cannot edit or reopen closed requests; ADMIN reopens them to an enabled non-closed status before editing. Block closure while dispatch or a linked loan is outstanding. All status changes and actual field edits write history atomically. Creation records the initial status with old_value null.

Inline `out_store` on request creation accepts shop_id and optional due_date/price/remarks. sent_date comes from service_date. Validate active profile, Out-Store enablement, mappings and shop first; allocate number, insert request/entry and history in one transaction. Return the created request and nullable out_store_entry. Failed dispatch rolls back the complete create, including number allocation.

Entry statuses are SENT and RECEIVED_BACK (UI labels Sent/Received Back). Require a company-wide shop or one belonging to the request's profile; only active shops may be newly selected. Allow one outstanding SENT entry per request, backed by a partial unique index. Sending applies the configured sent status; receipt records server time and applies the configured received status with history. Repeating receipt returns the existing outcome without duplicate history; later request status changes are not overwritten by the retry.

While SENT, PATCH can change dates/shop/price/remarks; status may only repeat its current value. All transitions to RECEIVED_BACK use receive-back. Completed entries are read-only. Block profile mapping changes, disabling Out-Store, or profile archival while dispatches are outstanding. Re-dispatch after receipt creates a new entry. Due date cannot precede sent date. No cancellation/backdated receipt contract is added.

## D11 Stand-by

Use AVAILABLE/ISSUED/UNDER_MAINTENANCE; new items are AVAILABLE. ADMIN may toggle an unissued item between AVAILABLE and UNDER_MAINTENANCE. Master PATCH cannot set ISSUED. Issue/return actions update item state and preserved issue history in one transaction, with a unique outstanding issue per item.

Issue requires customer_id and issued_date; optional service_request_id must belong to that customer/company. Optional received_product_id stores Product Received and must belong to the company; if linked to a request, its profile must match. This selection is independent of configurable linked-product fields and does not claim to identify a unique serial-numbered device.

Return requires explicit issue_id matching item/company. Repeating a return does not affect a newer issue. Return timestamp is server-generated; due_date is the planned return date and cannot precede issue date. Notes and previous product/customer references are retained. Prices are optional reference amounts; there are no deposits, payments or accounting entries.

## D12 Time audit and frontend

Calendar fields use YYYY-MM-DD. Event timestamps are stored as timestamptz and returned as RFC 3339 UTC; UI timestamps use local display. One history row per actual changed field stores stable key, real JSON old/new values, actor and timestamp; preserve false/zero/null. Reserve `core.` in audit keys; no history edit/delete API. Keep current customer details and current master labels on read; historical label/contact snapshots are not introduced.

Frontend location is root `ui/`, separate from api/. No frontend implementation occurs in T01. Financial presets remain optional number/formula data; notifications remain deferred.

## Implementation reconciliation

T03–T18 implementation and T23 backend verification are complete. Handover documentation remains.

1. Complete T24 handover documentation.

T02–T23 verification passes. T24 handover documentation is next. Later contract changes should be explicit revisions.

## Change log

- **4 October 2026 (T29):** users can reset a forgotten password from an emailed single-use link valid for 30 minutes. See [authentication decisions](authentication-decisions.md#forgot-password-t29-4-october-2026).
- **4 October 2026 (T28):** administrators no longer type passwords for new users; the server generates a temporary password, emails it and forces a change at first sign-in; administrators can reset a user to a new temporary password. See [authentication decisions](authentication-decisions.md#temporary-passwords-t28-4-october-2026).
- **4 October 2026 (T26):** passwords need 10–128 characters with at least one letter and one number (was 15–128 with no composition rule). Existing passwords keep working; the rule applies when a password is set.
- **4 October 2026 (T25):** users may sign in with email or an optional, globally unique phone number; see [authentication decisions](authentication-decisions.md#email-or-phone-sign-in-t25-4-october-2026).

