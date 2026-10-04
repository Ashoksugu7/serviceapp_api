# Request records and history

T16 implements company-scoped request listing, detail, editing, status changes and immutable history.

- Lists support validated pagination, stable sorting, request/customer/profile/status/date filters and text search across request number, customer identity and dynamic JSON values.
- Detail responses include the current customer, archived-capable form definition and current labels for linked products, charges and staff. Inactive links remain visible with `selectable=false`.
- PATCH merges supplied dynamic keys, preserves omitted and disabled historical values, removes optional values submitted as null, validates the effective form and writes one history row per actual changed key.
- Company, profile, number and creator remain immutable. Customer changes are blocked after any Out-Store or linked stand-by history exists.
- Closed records cannot be edited. USER cannot reopen a closed record; ADMIN can move it to an enabled status before editing. Closing is blocked while a dispatch or stand-by issue is outstanding.
- Status changes validate enabled same-profile statuses and write history atomically. Repeating the current status produces no duplicate history.

PostgreSQL-backed HTTP tests cover filtering, detail projections, linked labels, edits, history, closed-record behavior, ADMIN reopen and outstanding-dispatch closure protection. The application database has not been migrated.
