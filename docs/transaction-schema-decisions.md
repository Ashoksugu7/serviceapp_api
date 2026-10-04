# Transaction schema decisions

These choices implement T04 against the finalized [T01 requirements](t01-requirements.md). The application database has not been migrated.

## Service requests and history

- A service request stores the company, profile, customer, profile status, service date and creating company user as relational columns. Profile-specific values remain in a required JSON object in `form_data`.
- Request numbers follow the confirmed `{PREFIX}{NUMBER}` format and are unique within a company. T15 will lock and increment the profile counter in the same transaction that creates the request.
- Composite foreign keys require the profile, customer, status and creator to belong to the request company. The status must also belong to the selected profile. A SUPER_ADMIN has no company and therefore cannot author a service transaction.
- History stores one changed field per row. `core.<key>` identifies a core field and an unprefixed stable field key identifies a dynamic value. Nullable JSON old/new values preserve additions and removals; JSON `false`, zero and empty values remain distinguishable from SQL `NULL`.
- Archived profiles and disabled statuses remain referenced. The migration uses no cascading deletes.

## Out-Store lifecycle

- Entries use exactly `SENT` and `RECEIVED_BACK`. A sent entry has no receipt timestamp; a received entry requires one.
- A partial unique index permits only one outstanding `SENT` entry for a service request. A later dispatch is allowed after the previous entry is received.
- The request and shop must belong to the entry company. Due dates cannot precede sent dates, prices cannot be negative and remarks are limited to 4,000 characters.
- T17 must check profile Out-Store enablement and shop/profile compatibility, apply mapped request statuses, write history and update the entry atomically. It must also make receive-back retry behavior idempotent.

## Stand-by lifecycle

- An issue records the stand-by item, customer, optional service request, optional Product Received, dates and notes. Product Received is a relational product reference, as confirmed in T01.
- A linked request must belong to the issue customer and company. The item and received product must also belong to the company.
- A partial unique index permits only one unreturned issue for an item. The same item can be issued again after `returned_at` is set. Due dates cannot precede issue dates and notes are limited to 4,000 characters.
- T18 must validate Product Received against the linked request profile when applicable, synchronize the stand-by master status, reject closed-request changes, and perform issue/return updates atomically and idempotently.

## Indexes

- Service-request filters cover company, profile, status and service date, with additional customer and creator indexes and a GIN index for JSON form values.
- History is indexed by request/time, company/request and actor.
- Out-Store entries are indexed by company/status, request and shop. Stand-by issues are indexed by item/date, customer, request and received product.

## Verification

The SQL test covers all 17 T03/T04 tables, UUID primary keys, required tenant columns, request numbering, JSON shape, history values, cross-tenant rejection, profile/status consistency, Out-Store lifecycle constraints, Product Received, linked request/customer consistency, stand-by issue lifecycle, archive retention and the complete down/reapply chain.

The test runs in a unique schema inside a transaction and rolls back all changes. PostgreSQL 14.20 verification passed. The application database remains unchanged.
