# Phase 1 API contract

**Status: T01 implementation baseline.** The filename is retained for existing links. The machine-readable source is [openapi.json](../contracts/openapi.json); [T01 decisions](t01-requirements.md) define cross-record validation and business rules that JSON Schema cannot express. This contract describes target behavior; business endpoints are not yet implemented.

## Conventions

- Base path `/api/v1`, JSON, Bearer JWT. Scope/actor come from authenticated identity. Unknown body properties are rejected; PATCH must contain at least one property.
- GET/PATCH/PUT and successful receipt/return actions use 200. Creation uses 201; logout/archive/disable use 204 without a body. No universal Location header is promised for collections without a resource GET route. HEAD responses never contain bodies.
- Single objects are unwrapped. Lists return `{items, page, page_size, total}`; default page 1/page_size 25, maximum 100. All lists use allowlisted sorting and UUID tie-breakers; OpenAPI specifies each default and filter.
- Dates are YYYY-MM-DD; timestamps RFC 3339 UTC. Prices are nullable decimal strings with at most two decimal places. Dynamic numeric values are JSON numbers, with profile-aware validation and server formula evaluation.
- Bodies are limited to 1 MiB. Names/labels: 200 characters; email: 254; contact: 50; free text: 4000; field keys: 64. UUID formats, required/nullable fields and array limits are explicit in OpenAPI.
- Omitted PATCH properties preserve existing data; null clears only nullable values. `form_data` merges submitted keys, while field `config` replaces the complete config object when supplied. Core label PATCH merges named labels.

## Errors

```json
{"error":{"code":"VALIDATION_ERROR","message":"Invalid request","fields":{"complaint":"required"}}}
```

| HTTP | Codes / meaning |
| --- | --- |
| 400 | INVALID_JSON or VALIDATION_ERROR |
| 401 | UNAUTHENTICATED or INVALID_CREDENTIALS |
| 403 | FORBIDDEN or COMPANY_SUSPENDED |
| 404 | NOT_FOUND within authorized tenant scope |
| 405 | METHOD_NOT_ALLOWED with Allow |
| 409 | DUPLICATE, RESOURCE_IN_USE or INVALID_STATE |
| 413 | PAYLOAD_TOO_LARGE |
| 429 | RATE_LIMITED at login; Retry-After seconds |
| 500 | INTERNAL_ERROR; no internal/database details |
| 503 | NOT_READY or REQUEST_TIMEOUT |

## Roles

USER reads masters and forms and performs company service transactions. ADMIN manages its company's users, masters, configuration and transactions. SUPER_ADMIN manages companies, users and profiles/forms; it does not perform business-master or transaction operations. Form metadata can expose staff-role labels needed for profile administration without exposing staff records. Operation-level `x-roles` in OpenAPI records this matrix; runtime authorization remains required.

## Contracts by area

| Area | Input and response rules |
| --- | --- |
| Login/me/logout | Login takes email/password, returns token/token_type/expires_in/user/company; me returns user/company; logout revokes the presented session. No password/hash in responses. |
| Companies | Onboarding takes name/code/optional contacts and required initial admin object; returns `{company, admin}`. Only SUPER_ADMIN changes status. Code is immutable. |
| Users | Create takes name/email/password/ADMIN-or-USER role. PATCH can update identity/password/role/status within last-admin protections. |
| Customers | Require name/contact; customer_no is generated as C-1001 onward, never caller-supplied. Optional email/address. |
| Staff/roles | Require staff name/contact; role_ids atomically replace assignments. Roles support list/create/delete, with system/reference protections. |
| Products/charges/shops | Products/charges have immutable profile_id. Shops optionally have an immutable profile_id; null means company-wide. Product statuses ACTIVE/DISCONTINUED, staff/charges/shops ACTIVE/INACTIVE. |
| Stand-by masters | Name required, serial/category/price optional. Status AVAILABLE/ISSUED/UNDER_MAINTENANCE. Master PATCH cannot set ISSUED. |
| Profiles | Create name/prefix; seed core controls and custom default statuses with Out-Store disabled. Enable later through PATCH after mapping setup. Prefix cannot change after requests exist. Includes core_labels and counter starting 1001. |
| Dynamic fields | Create key/label/type/config, optionally required/enabled/sort_order. Per-type configs are discriminated schemas. PATCH cannot change key/is_system. Disable retains history. |
| Statuses/mappings | Status name/order/initial/closed/enabled; setting initial clears previous initial transactionally. Mapping must contain two valid status IDs or two nulls when disabled. |
| Form | Profile id/name/prefix plus four core_fields, all enabled dynamic fields, statuses and staff_roles metadata. No pagination within complete form definitions. |
| Service requests | Create profile_id/customer_id/service_date/form_data with optional inline out_store. Return request plus nullable out_store_entry. Number/status/creator are server-owned. PATCH allows service_date/customer_id/form_data under workflow restrictions. |
| Record detail | Request plus form_definition, customer and linked_values map. Historical disabled definitions and inactive linked labels remain visible. Labels/contact reflect current masters; no snapshot versioning. |
| History | Paginated immutable rows with stable field key, JSON old/new values, actor and timestamp. Creation logs initial status; no-op edits do not create history. |
| Out-Store | Create request/shop/sent_date plus optional due_date/price/remarks. SENT → RECEIVED_BACK via receive-back only. Actions return current resulting service_status_id. |
| Stand-by issue/return | Issue requires customer_id/issued_date, with optional service_request_id/received_product_id/due_date/notes. Return requires issue_id. Both return `{item, issue}`. |

## Reference-specific decisions

- Seed A/RF prefixes, 24 dynamic presets and statuses from the HTML. Request display uses A1001/RF1001; allocate counters transactionally. Protect Date/Customer No/Customer Name/Contact No as core controls.
- Financial presets remain informational numbers/formulas. Notifications are deferred entirely.
- Inline dispatch accepts `{shop_id, due_date?, price?, remarks?}` in `out_store`; sent_date uses service_date. Request, dispatch, status history and counter commit or roll back together.
- Product Received is optional received_product_id, scoped to the company and, when linked, request profile. It is a master-product reference, not a unique device serial record.
- Formula uses `{a, op, b}`, not an expression string. Two numeric field references, + - * /, server-side decimal evaluation, six-place half-away-from-zero rounding, cycle/depth validation and explicit division-by-zero errors. Required operands must be provided; missing optional ones become zero.
- Choice supports arrays for multiple; date supports toggle `{on,date}` and quick picks 0/3/7/14 days. Toggle on requires a valid date; off uses null. Complete type/config schemas are in OpenAPI.

## Verification

The contract covers 60 operations from API v3 and GET/HEAD for two operational health routes: 64 total across 40 paths. [Contract validation](../tests/validate_contract.py) checks OpenAPI structure, source route inventory, role restrictions and 38 positive/negative schema cases. It does not prove implementation behavior, transaction safety or UI parity; those remain the relevant implementation tasks.
