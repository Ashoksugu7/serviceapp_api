package repository

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// Out-Store and stand-by activity is written to the record history alongside
// field and status changes (T35, T36). Values are snapshots taken in the same
// transaction, so the history still reads correctly after shops or items are
// renamed. History created_at defaults to clock_timestamp() (migration 000007),
// so rows keep the order they were written in within a transaction.

const (
	historyOutStore = "core.out_store"
	historyStandby  = "core.standby"
)

func insertEvent(ctx context.Context, tx pgx.Tx, companyID, requestID, key string, oldValue, newValue map[string]any, actorID string) error {
	var oldJSON []byte
	if oldValue != nil {
		oldJSON, _ = json.Marshal(oldValue)
	}
	newJSON, _ := json.Marshal(newValue)
	_, err := tx.Exec(ctx, `INSERT INTO service_request_history(company_id,request_id,field_key,old_value,new_value,changed_by)VALUES($1,$2,$3,$4,$5,$6)`, companyID, requestID, key, oldJSON, newJSON, actorID)
	return err
}

func outStoreSnapshot(ctx context.Context, tx pgx.Tx, companyID, entryID string) (map[string]any, string, error) {
	var raw []byte
	var requestID string
	err := tx.QueryRow(ctx, `SELECT jsonb_build_object('entry_id',e.id,'shop_id',e.shop_id,'shop_name',s.shop_name,'sent_date',e.sent_date,'due_date',e.due_date,'price',e.price::text,'remarks',e.remarks,'received_back_at',e.received_back_at),e.service_request_id::text FROM out_store_entries e JOIN out_store_shops s ON s.company_id=e.company_id AND s.id=e.shop_id WHERE e.company_id=$1 AND e.id=$2`, companyID, entryID).Scan(&raw, &requestID)
	if err != nil {
		return nil, "", err
	}
	var snapshot map[string]any
	err = json.Unmarshal(raw, &snapshot)
	return snapshot, requestID, err
}

// recordOutStore writes a "sent" or "received" event for the entry.
func recordOutStore(ctx context.Context, tx pgx.Tx, companyID, entryID, event, actorID string) error {
	snapshot, requestID, err := outStoreSnapshot(ctx, tx, companyID, entryID)
	if err != nil {
		return err
	}
	snapshot["event"] = event
	return insertEvent(ctx, tx, companyID, requestID, historyOutStore, nil, snapshot, actorID)
}

// recordOutStoreChange writes a "changed" event with the before and after
// values; an edit that changes nothing is not recorded.
func recordOutStoreChange(ctx context.Context, tx pgx.Tx, companyID, entryID string, before map[string]any, actorID string) error {
	after, requestID, err := outStoreSnapshot(ctx, tx, companyID, entryID)
	if err != nil {
		return err
	}
	if jsonEqual(before, after) {
		return nil
	}
	after["event"] = "changed"
	return insertEvent(ctx, tx, companyID, requestID, historyOutStore, before, after, actorID)
}

// recordStandby writes an "issued" or "returned" event when the issue is
// linked to a service record. days_out is set once the item is back.
func recordStandby(ctx context.Context, tx pgx.Tx, companyID, issueID, event, actorID string) error {
	var raw []byte
	var requestID *string
	err := tx.QueryRow(ctx, `SELECT jsonb_build_object('event',$3::text,'issue_id',si.id,'standby_item_id',si.standby_item_id,'item_name',i.name,'serial_no',i.serial_no,'customer_name',c.name,'customer_contact',c.contact,'issued_date',si.issued_date,'due_date',si.due_date,'returned_at',si.returned_at,'days_out',`+standbyDaysOut+`),si.service_request_id::text FROM standby_item_issues si JOIN standby_items i ON i.company_id=si.company_id AND i.id=si.standby_item_id JOIN customers c ON c.company_id=si.company_id AND c.id=si.customer_id WHERE si.company_id=$1 AND si.id=$2`, companyID, issueID, event).Scan(&raw, &requestID)
	if err != nil || requestID == nil {
		return err
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	return insertEvent(ctx, tx, companyID, *requestID, historyStandby, nil, value, actorID)
}

// standbyDaysOut counts calendar days from issue to return; null while out.
const standbyDaysOut = `CASE WHEN si.returned_at IS NULL THEN NULL ELSE GREATEST(si.returned_at::date-si.issued_date,0) END`
