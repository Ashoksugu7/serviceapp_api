# Company, master data and dynamic forms

Tasks T07–T14 implement the authenticated company administration, master-data and dynamic-form routes in the validated OpenAPI contract.

- Company onboarding creates the company, first ADMIN and one blank **Service** profile (prefix `S`) with the default statuses in one transaction. Since T30 (4 October 2026) it no longer seeds the Job Card/Refill profiles, their 24 preset fields or any staff roles; admins create their own. Existing companies keep their data.
- Every new profile gets the default statuses (T31), in order: **Open** (initial), **In Progress**, **Sent to Out-Store**, **Received from Out-Store**, **Closed** (closed), **Returned Not Repaired** (closed). Out-Store sent/received are mapped to the two Out-Store statuses, so Out-Store can be switched on without extra setup; it stays off until enabled.
- Customers use a locked company counter for `C-{number}` allocation. User password changes revoke active sessions, and the final active company ADMIN cannot be deactivated or demoted.
- Company IDs come from the authenticated route scope. Composite database relationships reject cross-company profiles, roles and master references.
- ADMIN writes and USER reads follow the T01 matrix. SUPER_ADMIN is limited to company, user and profile/form administration.
- Staff role assignments replace atomically. Profile deletion archives and field deletion disables; historical rows remain referenced.
- T32 (4 October 2026): `DELETE …/statuses/{id}` deletes a status only when no record uses it now or in its history and it is neither the initial status nor mapped for Out-Store. Otherwise it returns 409 with a message such as `"Quoted" is the current status of 3 records, so it cannot be deleted. Disable it instead…`. Disabling stays `PATCH {"enabled": false}`.
- Form responses expose three core controls (date, customer mobile number, customer name; T33), enabled fields/statuses and staff-role metadata. Stable database names are mapped to API `key`, `type`, `initial` and `closed` properties.
- Dynamic definitions reject reserved core keys, invalid field types and invalid choice/staff-role configuration. The reusable form-data validator checks required, unknown, disabled and type-invalid submitted values for T15.

Repository and HTTP integration tests run in disposable PostgreSQL schemas. The application database has not been migrated.

## Server-side lists (T37, 4 October 2026)

Companies, users, customers, staff, staff roles, products, charges, Out-Store shops, stand-by items, service profiles, and each profile's fields and statuses are paged on the server: `page` (default 1), `page_size` (1–100, default 25), `q`, `sort`, `order` and the documented filters (`status`, `role`, `profile_id`, `is_active`, `enabled`). Responses carry the real `total`.

- `q` matches the list's text columns case-insensitively; `%` and `_` match literally. Customer, staff, user and shop mobile numbers also match by digits, so `98765 43210` and `9876543210` find the same record.
- Default sort is by name (shops by `shop_name`, fields and statuses by `sort_order`); ties break by ID so pages are stable.
- `profile_id` on shops also returns shops available to every profile.
- An unknown parameter, sort, order or filter value returns 400 naming the parameter.

Out-Store list and detail rows (T38) add `request_no`, `profile_id`, `profile_name`, `customer_id`, `customer_name`, `customer_contact`, `shop_name`, `shop_contact` and `overdue`; the list also accepts `q` (record number, customer, mobile digits or shop).
