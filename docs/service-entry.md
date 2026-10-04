# Service Entry

T15 implements authenticated Service Entry creation at `POST /api/v1/companies/{companyId}/service-requests`.

- The repository locks the active profile, reads its prefix/counter and increments the counter in the same transaction as the request insert. Failed validation or inserts roll back the counter.
- The initial enabled profile status is resolved by the server. The authenticated company user is always the creator; body-supplied company, status, number and actor values are not accepted.
- Form data is checked against the selected profile for required, enabled, known and correctly typed fields. Product, charge and role-based staff selections must be active and eligible in the same company/profile.
- An initial status history row is written with the request.
- Optional inline Out-Store validates profile enablement, status mapping and active compatible shop, creates the dispatch, applies the sent status and writes the status history in the same transaction.
- PostgreSQL-backed HTTP tests cover normal creation, required-field errors, failed-create counter rollback, inline dispatch and ten simultaneous creates with unique sequential numbers.

The application database has not been migrated.

## Inline customer (added 24 September 2026)

`POST /companies/{companyId}/service-requests` accepts either `customer_id` or a `customer` object (`name`, `contact`, optional `email` and `address`). An inline customer is validated together with the form, receives the next company customer number and is inserted in the same transaction as the request, so a failed request never leaves a customer behind. The response includes the created `customer` (or `null` when `customer_id` was used).

A mobile number whose digits match an existing company customer is rejected (`customer.contact: already belongs to Ravi Kumar; select that customer instead`), as are new or edited customers in the Customers master with a number already in use. This lets company USERs record walk-in customers during Service Entry while the Customers master itself stays ADMIN-only for writes.
