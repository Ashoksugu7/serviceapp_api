# Stand-by issue and return workflow

T18 implements company-scoped stand-by issue and return actions.

- Issue accepts a customer, issue date, optional due date, notes, service request and Product Received. The item must be `AVAILABLE`; `ISSUED` and `UNDER_MAINTENANCE` items cannot be issued.
- A linked request must be open and belong to the selected customer and company. Product Received must belong to the company and, when a request is linked, use that request's service profile.
- Issue history insertion and the item transition to `ISSUED` commit in one transaction. Item row locking and the partial unique index prevent concurrent outstanding issues.
- Return requires the exact issue ID for the item and company. It records a server timestamp and changes the item to `AVAILABLE` in the same transaction.
- Repeating a completed return is idempotent. If the item has since been issued again, retrying the older return leaves the newer issue and `ISSUED` state intact.
- ADMIN can move an unissued item between `AVAILABLE` and `UNDER_MAINTENANCE`. Master PATCH cannot set `ISSUED` or change the status of an issued item.
- Service requests cannot close while a linked stand-by issue remains outstanding. All previous issue records remain available as preserved database history.

PostgreSQL-backed HTTP tests cover profile and tenant relationships, issue/return state transitions, closure protection, duplicate issue rejection, maintenance protection, repeated returns and reissue safety. The application database has not been migrated.

## Who lent it, and history (T36, 4 October 2026)

Migration `000007_record_history_events` adds `issued_by` and `returned_by` (users in the same company) to `standby_item_issues`. Issue and return record the acting user; issues made before the migration show no user.

- When an issue is linked to a service record, issue and return each write a `core.standby` history row on that record: `event` (`issued`, `returned`), `issue_id`, `standby_item_id`, `item_name`, `serial_no`, `customer_name`, `customer_contact`, `issued_date`, `due_date`, `returned_at` and `days_out` (calendar days, set on return). See `HistoryStandbyValue` in the contract.
- `GET …/standby-items/{id}/issues` (now in the contract) returns the item's issues with `item_name`, `customer_name`, `customer_contact`, `request_no`, `issued_by_name`, `returned_by_name`, `days_out` (to today while out) and `overdue`. Unknown items return 404.
- `GET …/standby-issues` returns the same rows across items, paged, filtered by `contact` (mobile digits, ignoring spaces), `customer_id`, `standby_item_id`, `service_request_id` and `state` (`open`/`returned`). Invalid values return 400.
