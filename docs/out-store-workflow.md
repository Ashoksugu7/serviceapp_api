# Out-Store workflow

T17 implements company-scoped Out-Store listing, dispatch, detail, sent-entry editing and receive-back.

- Dispatch validates an open request, enabled profile, complete status mapping and an active company-wide or same-profile shop. Entry creation, mapped request status and status history commit together.
- Only one `SENT` entry may exist for a request. A new dispatch is allowed after receipt.
- While sent, shop, sent/due dates, price and remarks may change. The status cannot transition through PATCH. Received entries are immutable.
- Receive-back records the server timestamp, applies the enabled received mapping and writes request history atomically. Repeating receive-back returns the existing outcome without changing a later request status or adding history.
- Listing supports pagination, sent-date ordering and request/status/shop filters.
- Profile mapping changes, disabling Out-Store, profile archival and mapped-status disabling are rejected while relevant open dispatches exist.

PostgreSQL-backed HTTP tests cover list/detail/edit, receive-back, retry idempotency, completed-entry protection, re-dispatch, duplicate-open rejection, request closure protection and configuration locks. The application database has not been migrated.

## Record history (T35, 4 October 2026)

Every dispatch (including one made from Service Entry), edit and receive-back writes a `core.out_store` row to the record's history, in the same transaction and with the acting user, even when the record's status does not change. `core.status` rows for the mapped status changes are kept as before and come first. Migration `000007_record_history_events` makes history `created_at` default to `clock_timestamp()`, so rows written in one transaction keep their order (previously a record created with an inline dispatch could list its status change before "created").

`new_value` is a snapshot: `event` (`sent`, `changed`, `received`), `entry_id`, `shop_id`, `shop_name`, `sent_date`, `due_date`, `price`, `remarks`, `received_back_at`. A `changed` row also has the previous snapshot (without `event`) in `old_value`; an edit that changes nothing is not recorded. Repeating receive-back adds nothing. See `HistoryOutStoreValue` in the contract. History rows now include `changed_by_name`, so any user sees who made each change.
