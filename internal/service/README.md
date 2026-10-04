# Business services

Authentication, password hashing, signed tokens, rate-limit keys and reusable role/company authorization are implemented here. Subsequent business services receive the authenticated tenant context and call repositories synchronously. Request numbering, record/history changes and Out-Store status changes must commit in one database transaction before returning success.

Every later company operation must call the shared authorization boundary with its explicit role list and route company ID.
