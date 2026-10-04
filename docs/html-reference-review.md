# Computer services HTML reference review

> **Superseded 4 October 2026 (T30, T31):** onboarding now creates one blank "Service" profile with the default statuses Open → In Progress → Sent to Out-Store → Received from Out-Store → Closed / Returned Not Repaired, and no staff roles or preset fields. The Job Card/Refill definitions below remain a reference for admins building their own profiles. See [management and forms](management-and-forms.md).


Reviewed [computer-services.html](../../computer-services.html) by inspecting its embedded JavaScript and markup. This is a source-level behavior review, not a browser interaction or visual QA pass. The application bundle and CSS are inline; external links supply the Inter font. No separate application script is required for this review.

The HTML is a React state-based prototype with sample records. Its bundled symbols below are searchable evidence locators; they are not proposed production names. The v3 documents remain authoritative for authentication, tenant isolation, relational references, historical preservation and Phase 2 exclusions.

## Navigation and scope

`Hh` and `vn` expose Service Entry, Out-Store Entry, Records, Notification Log, Customers, Staff, Products & Services, Out-Store Shops, Stand-by Items and Service Profiles. Dashboard and Settings are inactive labels. There is no login/company/role-management flow in the prototype; those remain requirements from the HLD/API documents and the user's confirmed role choices.

The prototype stores state with React `useState`; it does not establish persistence, server authorization, concurrency handling or API contracts. Sample customer/staff identities must not become production seed accounts.

## Core fields and profiles

`Xh`, `va` and the core-label editor establish four protected fields: `date` (Date), `customerNo` (Customer No), `customerName` (Customer Name), and `contact` (Contact No). Labels can change; these controls cannot be removed or retyped. In the backend contract, map these to service_date and customer selection/display projections; use relational customer_id rather than duplicating editable customer identity in form_data. Define core field metadata before seeding.

`$h`, `Rh`, `Yh`, `ex`, `xc`, `gc` and `Lc` define:

| Profile | Prefix | Out-Store default | Status options in order | Sent / received-back mapping |
| --- | --- | --- | --- | --- |
| Job Card (`jobcard`) | A | Enabled | Pending; Assigned; In-Progress; Completed; Sent to Out-Store; Delivered; Returned Not Repaired | Sent to Out-Store / Completed |
| Refill (`refill`) | RF | Disabled | Received; In-Progress; Completed; Sent; Delivered; Returned Not Repaired | Sent / Completed |
| New custom profile | First two sanitized letters of name, fallback C | Disabled | Pending; Sent to Out-Store; Completed; Delivered; Returned Not Repaired | Sent to Out-Store / Completed |

`In` uppercases prefixes and removes non-A–Z characters. `sx` creates custom profiles with the four core controls and no dynamic fields. It permits profile rename/prefix edits, deletes configuration while retaining records and an archived profile name, and prevents deleting the last profile. Its Reset Fields clears dynamic fields, restores core labels and disables Out-Store; it does not restore the Job Card/Refill field presets. Backend archive/reset behavior must preserve definitions needed by historical requests, as required by the v3 documents.

The HTML has status lists and hard-coded creation statuses, not explicit initial/closed flags. Job Card/custom creation uses Pending; Refill uses Received. Closure rules and initial-flag semantics remain backend decisions, not observations from the HTML.

## Default dynamic fields

`Jh` supplies the following ordered fields. All are enabled. Only the rows marked Yes are required. These are configurable dynamic fields, not protected core fields. API field types use the existing snake_case equivalents of the prototype's camelCase names. Preserve each prototype field ID as the immutable field_key when importing these presets; create a separate UUID row ID.

| Field key | Label | API type | Required | Configuration from reference |
| --- | --- | --- | --- | --- |
| jc_attendedby | Attended By | staff_role | No | Built-in Attended By role |
| jc_servicetype | Service Type | choice | Yes | In-Person, In-Store |
| jc_duedate | Due Date | date | No | Quick-pick |
| jc_product | Product | linked_product | No | Profile products |
| jc_serial | Serial No | text | No | Plain |
| jc_complaint | Complaint | text | Yes | Multiline |
| jc_accessories | Accessories | text | No | Plain |
| jc_address | Address | text | No | Plain |
| jc_advance | Advance Amount | number | No | Currency display; scope decision below |
| jc_totalamount | Total Amount | number | No | Currency display; scope decision below |
| jc_balance | Balance Due | number | No | Currency; jc_totalamount minus jc_advance |
| jc_services | Services Provided | linked_charges | No | Multi-select |
| jc_engineer | Service Engineer | staff_role | No | Built-in Service Engineer role |
| jc_deliverydate | Delivery Date | date | No | Quick-pick |
| jc_deliveredby | Delivered By | staff_role | No | Custom Delivered By role |
| jc_reminder | Reminder | date | No | Toggle-based |
| rf_attendedby | Attended By | staff_role | No | Built-in Attended By role |
| rf_toner | Toner Model | text | Yes | Plain |
| rf_engineer | Assigned Engineer | staff_role | No | Built-in Service Engineer role |
| rf_charges | Charges / Refilling | linked_charges | No | Multi-select |
| rf_totalamount | Total Amount | number | No | Currency display; scope decision below |
| rf_deliverydate | Delivery Date | date | No | Quick-pick |
| rf_deliveredby | Delivered By | staff_role | No | Custom Delivered By role |
| rf_remarks | Remarks | text | No | Plain |

## Field configuration and validation

`Cn`, `Cc`, `Ic`, `Kr`, `mc`, the field editor and `nx.Nc` establish:

- Text supports multiline and plain/phone/email formats. Phone/email use their corresponding input types; the HTML does not supply comprehensive server validation.
- Number supports currency display and a structured formula `{a: fieldKey, op: operator, b: fieldKey}`. This is a two-field calculation, not a free-form expression language. Formula output is read-only and displays at most two fractional digits; stored results are not explicitly rounded.
- `Ic` supports add/subtract/multiply/divide, coerces missing or unparsable operands to zero, and returns zero for division by zero. The editor encodes multiply/divide options differently from the evaluator's Unicode symbols; normalize to `*` and `/` in the API rather than copying that mismatch. Formula chains/cycles are not resolved by the prototype.
- Date supports ordinary date input, quick-pick, or toggle-based values `{on: boolean, date: string}`. Quick-pick and toggle mode are mutually exclusive. Quick picks are Today, +3 days, +7 days and +14 days. `Kr` uses date arithmetic followed by UTC ISO slicing; choose and document production timezone semantics separately.
- Choice supports one string or an array when `multiple` is enabled. Single choices can render as radio buttons using `buttons`; multiple selection disables that option.
- Checkbox stores a boolean; required validation accepts false if explicitly present. Required multi-select values must contain an item. Required toggle-date validation checks `on`, but does not require a populated date; production validation should address that gap explicitly.
- Linked product is profile-filtered but does not exclude discontinued products in `Cc`. Linked charges require active and same-profile choices. Staff selection requires Active status and membership in the configured built-in or custom role.
- Prototype linked values are display names; the v3 backend contract uses UUID references. Resolve display names for the UI, and validate tenant/profile/role eligibility on the server.

## Customers staff and numbering

`Vh` auto-generates customer numbers starting at C-1001 and requires name/contact; email/address are optional. Its MAX-based client calculation is demonstration logic. Production needs a company-scoped transactional counter and unique constraint. `Wh` requires staff name/contact and supports independent Attended By/Service Engineer flags plus arbitrary custom roles; Delivered By is an initial custom role.

`nx` generates request numbers as prefix plus `1001 + record count`, for example A1001 and RF1001, without a hyphen or zero-padding. Legacy sample records use other formats; they are not the generator contract. Production preserves this display shape with server-side atomic counters. T03 now defaults profile counters to 1001 instead of copying the prototype's array-count allocation.

Master vocabulary is not uniform: staff Active/Inactive; products include Discontinued; charges use an active boolean; shops Active; stand-by shows Available/Issued and a color mapping for Under Maintenance. An API can normalize codes while preserving these labels; do not equate every inactive product with the same prototype status.

## Service entry and records

`nx` selects customers by number and populates name/contact. It supports Copy to New, preserving customer/dynamic data while assigning a new date/number/history. It can create an Out-Store entry in the same submit action using shop, due date and price. The existing API create payload lacks this optional dispatch block and needs an explicit contract extension if this flow is retained.

`nx` writes an initial status-history entry. `mx`/`px` support status changes, inline dynamic edits, copy, and change history; no closed-record lock is enforced in the prototype. Search covers customer name/contact/number, request number and dynamic values, with a profile filter. The production history should keep immutable keys and actual JSON values rather than the prototype's label-based keys and falsy-value-to-dash conversion.

## Out-Store and stand-by

`fx` creates Sent entries and receives them as Received Back. Dispatch/receipt applies profile mappings; no duplicate outstanding-entry guard exists in the prototype. Its standalone shop selector filters using jobcard rather than the selected request's profile, which must be corrected by tenant/profile validation. Entry creation in `nx` hard-codes initial mapped status rather than always consulting mappings; production must use configuration consistently.

`ix` registers a loaner with name, optional serial/category/price; it tracks customer, Product Received, issue date and return date. Returning clears current issue fields on the item. DB v3 requires preserved issue history, so return must retain an issue row. The Product Received selector is absent from the DB/API draft and needs a dedicated product reference or explicitly agreed replacement. `returnDate` is collected before return; map that planned date to due_date, while returned_at records actual receipt.

## Differences needing decisions

| Difference | Proposed treatment / status |
| --- | --- |
| Notification templates, triggers and Notification Log (`ax`, `Sn`, `cx`) absent from API/schema | User confirmed deferral. The prototype appends local messages; it does not send SMS/email. Do not infer a delivery integration. |
| Advance/total/balance numeric fields despite Phase 2 financial exclusions | Retain as optional dynamic number/formula configuration only if desired; no payment/invoice/accounting behavior. User confirmed inclusion as informational fields. |
| Product Received on loan issue absent from DB/API | User confirmed Product Received; optional received_product_id and tenant/profile validation are in the final contract. |
| Inline Out-Store during Service Entry | User confirmed inline dispatch; optional out_store and atomic creation are in the final contract. |
| Customer/request numbering | Adopt observed display formats; add company customer counter and reconcile profile starting counter; do not copy client MAX/count allocation. |
| Destructive field/profile deletion/reset | Preserve backend archive/disable semantics from DB v3 and historical definitions. |
| Free editing, formulas and weak validation | Identify stronger backend rules as implementation decisions; do not describe them as HTML behavior. |

T01's reference input is now available and its fields/defaults are inventoried. T01 scope choices and technical contracts are now finalized in [requirements](t01-requirements.md) and [OpenAPI](../contracts/openapi.json); implementation and visual verification remain separate tasks.
