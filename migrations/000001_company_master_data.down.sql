-- Destructive rollback: execute only intentionally, in one transaction.
-- No CASCADE: later migrations must be reverted first.
DROP TABLE standby_items;
DROP TABLE out_store_shops;
DROP TABLE charges;
DROP TABLE products;
ALTER TABLE service_profiles DROP CONSTRAINT service_profiles_sent_status_fk;
ALTER TABLE service_profiles DROP CONSTRAINT service_profiles_received_status_fk;
DROP TABLE service_profile_statuses;
DROP TABLE service_profile_fields;
DROP FUNCTION protect_service_profile_field_key();
DROP TABLE service_profiles;
DROP TABLE staff_role_assignments;
DROP TABLE staff_roles;
DROP TABLE staff;
DROP TABLE customers;
DROP TABLE users;
DROP TABLE companies;
