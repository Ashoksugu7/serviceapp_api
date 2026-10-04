package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// createReferenceProfiles builds the Job Card (prefix A, Out-Store on) and
// Refill (prefix RF) profiles from the reference HTML through the API, the way
// an admin would now that onboarding no longer seeds them (T30). Both get the
// default statuses (T31). It returns the two profile IDs.
func createReferenceProfiles(t *testing.T, h http.Handler, token, companyID string) (jobCardID, refillID string) {
	t.Helper()
	base := "/api/v1/companies/" + companyID
	post := func(path, body string) map[string]any {
		t.Helper()
		response := httpJSON(t, h, http.MethodPost, base+path, token, body)
		if response.Code != http.StatusCreated {
			t.Fatalf("POST %s %d %s", path, response.Code, response.Body.String())
		}
		var created map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &created)
		return created
	}
	roles := map[string]string{}
	for _, name := range []string{"Attended By", "Service Engineer", "Delivered By"} {
		roles[name] = post("/staff-roles", fmt.Sprintf(`{"name":%q}`, name))["id"].(string)
	}
	staff := func(role string) string { return fmt.Sprintf(`{"role_id":%q}`, roles[role]) }

	jobCardID = post("/service-profiles", `{"name":"Job Card","prefix":"A"}`)["id"].(string)
	refillID = post("/service-profiles", `{"name":"Refill","prefix":"RF"}`)["id"].(string)
	if enabled := httpJSON(t, h, http.MethodPatch, base+"/service-profiles/"+jobCardID, token, `{"out_store_enabled":true}`); enabled.Code != http.StatusOK {
		t.Fatalf("enable Job Card Out-Store %d %s", enabled.Code, enabled.Body.String())
	}

	fields := map[string][][5]string{
		jobCardID: {
			{"jc_attendedby", "Attended By", "staff_role", "false", staff("Attended By")},
			{"jc_servicetype", "Service Type", "choice", "true", `{"options":["In-Person","In-Store"]}`},
			{"jc_duedate", "Due Date", "date", "false", `{"quick_pick":true}`},
			{"jc_product", "Product", "linked_product", "false", `{}`},
			{"jc_serial", "Serial No", "text", "false", `{}`},
			{"jc_complaint", "Complaint", "text", "true", `{"multiline":true}`},
			{"jc_accessories", "Accessories", "text", "false", `{}`},
			{"jc_address", "Address", "text", "false", `{}`},
			{"jc_advance", "Advance Amount", "number", "false", `{"currency":true}`},
			{"jc_totalamount", "Total Amount", "number", "false", `{"currency":true}`},
			{"jc_balance", "Balance Due", "number", "false", `{"currency":true,"formula":{"a":"jc_totalamount","op":"-","b":"jc_advance"}}`},
			{"jc_services", "Services Provided", "linked_charges", "false", `{}`},
			{"jc_engineer", "Service Engineer", "staff_role", "false", staff("Service Engineer")},
			{"jc_deliverydate", "Delivery Date", "date", "false", `{"quick_pick":true}`},
			{"jc_deliveredby", "Delivered By", "staff_role", "false", staff("Delivered By")},
			{"jc_reminder", "Reminder", "date", "false", `{"toggle_based":true}`},
		},
		refillID: {
			{"rf_attendedby", "Attended By", "staff_role", "false", staff("Attended By")},
			{"rf_toner", "Toner Model", "text", "true", `{}`},
			{"rf_engineer", "Assigned Engineer", "staff_role", "false", staff("Service Engineer")},
			{"rf_charges", "Charges / Refilling", "linked_charges", "false", `{}`},
			{"rf_totalamount", "Total Amount", "number", "false", `{"currency":true}`},
			{"rf_deliverydate", "Delivery Date", "date", "false", `{"quick_pick":true}`},
			{"rf_deliveredby", "Delivered By", "staff_role", "false", staff("Delivered By")},
			{"rf_remarks", "Remarks", "text", "false", `{"multiline":true}`},
		},
	}
	for profileID, list := range fields {
		for order, f := range list {
			post("/service-profiles/"+profileID+"/fields", fmt.Sprintf(`{"key":%q,"label":%q,"type":%q,"required":%s,"sort_order":%d,"config":%s}`, f[0], f[1], f[2], f[3], order, f[4]))
		}
	}
	return jobCardID, refillID
}
