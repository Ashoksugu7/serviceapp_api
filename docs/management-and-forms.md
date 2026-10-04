# Company, master data and dynamic forms

Tasks T07–T14 implement the authenticated company administration, master-data and dynamic-form routes in the validated OpenAPI contract.

- Company onboarding creates the company, first ADMIN, Job Card and Refill profiles, 14 statuses, three staff roles and all 24 reference fields in one transaction.
- Customers use a locked company counter for `C-{number}` allocation. User password changes revoke active sessions, and the final active company ADMIN cannot be deactivated or demoted.
- Company IDs come from the authenticated route scope. Composite database relationships reject cross-company profiles, roles and master references.
- ADMIN writes and USER reads follow the T01 matrix. SUPER_ADMIN is limited to company, user and profile/form administration.
- Staff role assignments replace atomically. Profile deletion archives and field deletion disables; historical rows remain referenced.
- T32 (4 October 2026): `DELETE …/statuses/{id}` deletes a status only when no record uses it now or in its history and it is neither the initial status nor mapped for Out-Store. Otherwise it returns 409 with a message such as `"Quoted" is the current status of 3 records, so it cannot be deleted. Disable it instead…`. Disabling stays `PATCH {"enabled": false}`.
- Form responses expose three core controls (date, customer mobile number, customer name; T33), enabled fields/statuses and staff-role metadata. Stable database names are mapped to API `key`, `type`, `initial` and `closed` properties.
- Dynamic definitions reject reserved core keys, invalid field types and invalid choice/staff-role configuration. The reusable form-data validator checks required, unknown, disabled and type-invalid submitted values for T15.

Repository and HTTP integration tests run in disposable PostgreSQL schemas. The application database has not been migrated.
