package httpapi

import "net/http"

// fixedEnums exposes application-defined dropdown values. Company and profile
// configuration (such as staff roles and service statuses) is intentionally
// excluded because it must be loaded from its tenant-scoped endpoint.
func fixedEnums(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"field_types":  []string{"text", "number", "date", "choice", "checkbox", "linked_product", "linked_charges", "staff_role"},
		"text_formats": []string{"plain", "phone", "email"},
		"user_roles":   []string{"SUPER_ADMIN", "ADMIN", "USER"},
		"statuses": map[string][]string{
			"company":         {"ACTIVE", "SUSPENDED"},
			"user":            {"ACTIVE", "INACTIVE"},
			"staff":           {"ACTIVE", "INACTIVE"},
			"product":         {"ACTIVE", "DISCONTINUED"},
			"charge":          {"ACTIVE", "INACTIVE"},
			"out_store_shop":  {"ACTIVE", "INACTIVE"},
			"standby_item":    {"AVAILABLE", "ISSUED", "UNDER_MAINTENANCE"},
			"out_store_entry": {"SENT", "RECEIVED_BACK"},
		},
	})
}
