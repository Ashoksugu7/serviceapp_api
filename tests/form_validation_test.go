package tests

import (
	"encoding/json"
	"testing"

	. "serviceops360/api/internal/service"
)

func TestValidateFormData(t *testing.T) {
	fields := []DynamicField{{Key: "complaint", Type: "text", Required: true, Enabled: true}, {Key: "urgent", Type: "checkbox", Enabled: true}, {Key: "status", Type: "choice", Enabled: true, Config: map[string]any{"options": []any{"Open", "Closed"}}}}
	errors := ValidateFormData(fields, map[string]any{"urgent": "yes", "status": "Other", "extra": 1})
	for _, key := range []string{"complaint", "urgent", "status", "extra"} {
		if errors[key] == "" {
			t.Fatalf("missing validation error for %s: %+v", key, errors)
		}
	}
}

func TestValidateFieldDefinition(t *testing.T) {
	if err := ValidateFieldDefinition(map[string]any{"key": "service_date", "type": "date", "config": map[string]any{}}, true); err == nil {
		t.Fatal("reserved key accepted")
	}
	if err := ValidateFieldDefinition(map[string]any{"key": "priority", "label": "Priority", "type": "choice", "config": map[string]any{"options": []any{"High", "Low"}}}, true); err != nil {
		t.Fatal(err)
	}
}

func TestValidateFieldConfigurations(t *testing.T) {
	valid := []map[string]any{
		{"key": "description", "label": "Description", "type": "text", "config": map[string]any{"multiline": true, "format": "plain", "max_length": float64(4000)}},
		{"key": "amount", "label": "Amount", "type": "number", "config": map[string]any{"currency": true}},
		{"key": "visit", "label": "Visit", "type": "date", "config": map[string]any{"toggle_based": true}},
		{"key": "priority", "label": "Priority", "type": "choice", "config": map[string]any{"options": []any{"High", "Low"}, "multiple": false, "buttons": true}},
	}
	for _, definition := range valid {
		if err := ValidateFieldDefinition(definition, true); err != nil {
			t.Fatalf("valid definition rejected: %+v: %v", definition, err)
		}
	}
	invalid := []map[string]any{
		{"key": "visit", "label": "Visit", "type": "date", "config": map[string]any{"quick_pick": true, "toggle_based": true}},
		{"key": "priority", "label": "Priority", "type": "choice", "config": map[string]any{"options": []any{"Same", "Same"}}},
		{"key": "amount", "label": "Amount", "type": "number", "config": map[string]any{"formula": map[string]any{"a": "x", "op": "eval", "b": "y"}}},
		{"key": "flag", "label": "Flag", "type": "checkbox", "config": map[string]any{"unknown": true}},
	}
	for _, definition := range invalid {
		if err := ValidateFieldDefinition(definition, true); err == nil {
			t.Fatalf("invalid definition accepted: %+v", definition)
		}
	}
}

func TestApplyFormulas(t *testing.T) {
	fields := []DynamicField{
		{Key: "amount", Type: "number", Enabled: true, Config: map[string]any{"currency": true}},
		{Key: "advance", Type: "number", Enabled: true, Config: map[string]any{"currency": true}},
		{Key: "balance", Type: "number", Enabled: true, Config: map[string]any{"currency": true, "formula": map[string]any{"a": "amount", "op": "-", "b": "advance"}}},
		{Key: "half", Type: "number", Enabled: true, Config: map[string]any{"formula": map[string]any{"a": "balance", "op": "/", "b": "two"}}},
		{Key: "two", Type: "number", Enabled: true, Config: map[string]any{}},
		{Key: "one", Type: "number", Enabled: true, Config: map[string]any{}},
		{Key: "six", Type: "number", Enabled: true, Config: map[string]any{}},
		{Key: "negative_one", Type: "number", Enabled: true, Config: map[string]any{}},
		{Key: "sixth", Type: "number", Enabled: true, Config: map[string]any{"formula": map[string]any{"a": "one", "op": "/", "b": "six"}}},
		{Key: "negative_sixth", Type: "number", Enabled: true, Config: map[string]any{"formula": map[string]any{"a": "negative_one", "op": "/", "b": "six"}}},
	}
	values := map[string]any{"amount": float64(10), "advance": float64(25), "two": float64(2), "one": float64(1), "six": float64(6), "negative_one": float64(-1)}
	if errors := ApplyFormulas(fields, values, values); len(errors) != 0 {
		t.Fatal(errors)
	}
	if values["balance"] != json.Number("-15") || values["half"] != json.Number("-7.5") {
		t.Fatalf("unexpected formula values: %+v", values)
	}
	if values["sixth"] != json.Number("0.166667") || values["negative_sixth"] != json.Number("-0.166667") {
		t.Fatalf("half-away rounding failed: %+v", values)
	}
	if errors := ApplyFormulas(fields, values, map[string]any{"balance": 1}); errors["balance"] == "" {
		t.Fatalf("calculated field override accepted: %+v", errors)
	}
	cyclic := []DynamicField{
		{Key: "a", Type: "number", Enabled: true, Config: map[string]any{"formula": map[string]any{"a": "b", "op": "+", "b": "source"}}},
		{Key: "b", Type: "number", Enabled: true, Config: map[string]any{"formula": map[string]any{"a": "a", "op": "+", "b": "source"}}},
		{Key: "source", Type: "number", Enabled: true, Config: map[string]any{}},
	}
	if errors := ApplyFormulas(cyclic, map[string]any{"source": float64(1)}, map[string]any{}); len(errors) == 0 {
		t.Fatal("formula cycle accepted")
	}
}

func TestValidateMultipleChoiceAndToggleDate(t *testing.T) {
	fields := []DynamicField{
		{Key: "choices", Type: "choice", Required: true, Enabled: true, Config: map[string]any{"options": []any{"A", "B"}, "multiple": true}},
		{Key: "reminder", Type: "date", Enabled: true, Config: map[string]any{"toggle_based": true}},
	}
	valid := map[string]any{"choices": []any{"A", "B"}, "reminder": map[string]any{"on": false, "date": nil}}
	if errors := ValidateFormData(fields, valid); len(errors) != 0 {
		t.Fatal(errors)
	}
	invalid := map[string]any{"choices": []any{"A", "A"}, "reminder": map[string]any{"on": true, "date": nil}}
	if errors := ValidateFormData(fields, invalid); len(errors) != 2 {
		t.Fatalf("invalid values accepted: %+v", errors)
	}
}
