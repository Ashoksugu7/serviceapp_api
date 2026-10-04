# Repositories

The authentication repository locks users during login issuance, persists/revokes sessions, reloads current identity state and atomically records shared login-failure windows. Every later company-owned lookup and write must include the authorized company ID, including checks for linked records. Transactional operations must share the same transaction across all related writes.

Pool setup lives in `../database`.
