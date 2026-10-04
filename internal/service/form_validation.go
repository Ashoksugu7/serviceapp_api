package service

import (
	"encoding/json"
	"fmt"
	"math"
	"net/mail"
	"strings"
	"time"
)

var fieldTypes = map[string]bool{"text": true, "number": true, "date": true, "choice": true, "checkbox": true, "linked_product": true, "linked_charges": true, "staff_role": true}
var coreKeys = map[string]bool{"service_date": true, "customer_no": true, "customer_name": true, "customer_contact": true}

func ValidateFieldDefinition(value map[string]any, creating bool) error {
	if creating {
		key, ok := value["key"].(string)
		if !ok || coreKeys[key] || len(key) > 64 {
			return fmt.Errorf("%w: invalid or reserved field key", ErrValidation)
		}
		label, labelOK := value["label"].(string)
		kind, kindOK := value["type"].(string)
		if !labelOK || strings.TrimSpace(label) == "" || len(label) > 200 || !kindOK || !fieldTypes[kind] {
			return fmt.Errorf("%w: key, label, type and config are required", ErrValidation)
		}
		if _, ok := value["config"].(map[string]any); !ok {
			return fmt.Errorf("%w: config must be an object", ErrValidation)
		}
	}
	if raw, present := value["label"]; present {
		label, ok := raw.(string)
		if !ok || strings.TrimSpace(label) == "" || len(label) > 200 {
			return fmt.Errorf("%w: invalid field label", ErrValidation)
		}
	}
	for _, key := range []string{"required", "enabled"} {
		if raw, present := value[key]; present {
			if _, ok := raw.(bool); !ok {
				return fmt.Errorf("%w: %s must be boolean", ErrValidation, key)
			}
		}
	}
	if raw, present := value["sort_order"]; present {
		order, ok := integerValue(raw)
		if !ok || order < 0 {
			return fmt.Errorf("%w: invalid sort_order", ErrValidation)
		}
	}
	if raw, ok := value["type"]; ok {
		kind, ok := raw.(string)
		if !ok || !fieldTypes[kind] {
			return fmt.Errorf("%w: invalid field type", ErrValidation)
		}
	}
	if raw, ok := value["config"]; ok {
		config, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%w: config must be an object", ErrValidation)
		}
		kind, hasKind := value["type"].(string)
		if hasKind {
			if err := validateFieldConfig(kind, config); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateFieldConfig(kind string, config map[string]any) error {
	allowed := map[string]map[string]bool{
		"text": {"multiline": true, "format": true, "max_length": true}, "number": {"currency": true, "formula": true},
		"date": {"quick_pick": true, "toggle_based": true}, "choice": {"options": true, "multiple": true, "buttons": true},
		"checkbox": {}, "linked_product": {}, "linked_charges": {}, "staff_role": {"role_id": true},
	}
	keys, known := allowed[kind]
	if !known {
		return fmt.Errorf("%w: invalid field type", ErrValidation)
	}
	for key := range config {
		if !keys[key] {
			return fmt.Errorf("%w: unsupported %s config", ErrValidation, kind)
		}
	}
	boolValue := func(key string) (bool, bool) {
		raw, present := config[key]
		if !present {
			return false, true
		}
		value, ok := raw.(bool)
		return value, ok
	}
	switch kind {
	case "text":
		if _, ok := boolValue("multiline"); !ok {
			return fmt.Errorf("%w: multiline must be boolean", ErrValidation)
		}
		if raw, present := config["format"]; present {
			format, ok := raw.(string)
			if !ok || (format != "plain" && format != "phone" && format != "email") {
				return fmt.Errorf("%w: invalid text format", ErrValidation)
			}
		}
		if raw, present := config["max_length"]; present {
			length, ok := integerValue(raw)
			if !ok || length < 1 || length > 4000 {
				return fmt.Errorf("%w: invalid text max_length", ErrValidation)
			}
		}
	case "number":
		if _, ok := boolValue("currency"); !ok {
			return fmt.Errorf("%w: currency must be boolean", ErrValidation)
		}
		if raw, present := config["formula"]; present && raw != nil {
			formula, ok := raw.(map[string]any)
			if !ok || len(formula) != 3 {
				return fmt.Errorf("%w: invalid formula", ErrValidation)
			}
			a, aOK := formula["a"].(string)
			b, bOK := formula["b"].(string)
			op, opOK := formula["op"].(string)
			if !aOK || !bOK || !opOK || !validFieldKey(a) || !validFieldKey(b) || (op != "+" && op != "-" && op != "*" && op != "/") {
				return fmt.Errorf("%w: invalid formula", ErrValidation)
			}
		}
	case "date":
		quick, quickOK := boolValue("quick_pick")
		toggle, toggleOK := boolValue("toggle_based")
		if !quickOK || !toggleOK || (quick && toggle) {
			return fmt.Errorf("%w: invalid date config", ErrValidation)
		}
	case "choice":
		options, ok := config["options"].([]any)
		if !ok || len(options) == 0 || len(options) > 100 {
			return fmt.Errorf("%w: choice requires up to 100 options", ErrValidation)
		}
		seen := map[string]bool{}
		for _, option := range options {
			text, ok := option.(string)
			if !ok || strings.TrimSpace(text) == "" || len(text) > 200 || seen[text] {
				return fmt.Errorf("%w: choice options must be distinct nonempty strings", ErrValidation)
			}
			seen[text] = true
		}
		multiple, multipleOK := boolValue("multiple")
		buttons, buttonsOK := boolValue("buttons")
		if !multipleOK || !buttonsOK || (multiple && buttons) {
			return fmt.Errorf("%w: invalid choice config", ErrValidation)
		}
	case "staff_role":
		id, ok := config["role_id"].(string)
		if !ok || len(config) != 1 || !validUUID(id) {
			return fmt.Errorf("%w: staff_role requires role_id", ErrValidation)
		}
	}
	return nil
}

func validFieldKey(value string) bool {
	if len(value) == 0 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value[1:] {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func integerValue(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		if typed == math.Trunc(typed) {
			return int(typed), true
		}
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err == nil
	case int:
		return typed, true
	}
	return 0, false
}

type DynamicField struct {
	Key, Type         string
	Required, Enabled bool
	IsSystem          bool
	Config            map[string]any
}

func ValidateFormData(fields []DynamicField, values map[string]any) map[string]string {
	errorsByField := map[string]string{}
	definitions := map[string]DynamicField{}
	for _, field := range fields {
		definitions[field.Key] = field
		if field.Required && field.Enabled {
			value, present := values[field.Key]
			if !present || value == nil || (field.Type == "text" && strings.TrimSpace(fmt.Sprint(value)) == "") {
				errorsByField[field.Key] = "required"
			}
		}
	}
	for key, value := range values {
		field, known := definitions[key]
		if !known {
			errorsByField[key] = "unknown field"
			continue
		}
		if !field.Enabled {
			errorsByField[key] = "field is disabled"
			continue
		}
		if value == nil {
			continue
		}
		valid := false
		switch field.Type {
		case "text":
			text, ok := value.(string)
			valid = ok
			if valid {
				if max, ok := integerValue(field.Config["max_length"]); ok && len(text) > max {
					valid = false
				}
				if format, _ := field.Config["format"].(string); format == "email" {
					_, err := mail.ParseAddress(text)
					valid = err == nil
				}
			}
		case "number":
			number, ok := numericValue(value)
			valid = ok && math.Abs(number) <= 9999999999.99
			_, calculated := field.Config["formula"].(map[string]any)
			if currency, _ := field.Config["currency"].(bool); currency && !calculated && number < 0 {
				valid = false
			}
		case "date":
			if toggle, _ := field.Config["toggle_based"].(bool); toggle {
				object, ok := value.(map[string]any)
				on, onOK := object["on"].(bool)
				date, datePresent := object["date"]
				valid = ok && onOK && len(object) == 2 && datePresent
				if valid && on {
					text, textOK := date.(string)
					_, err := time.Parse("2006-01-02", text)
					valid = textOK && err == nil
				} else if valid {
					valid = date == nil
				}
			} else {
				text, ok := value.(string)
				if ok {
					_, err := time.Parse("2006-01-02", text)
					valid = err == nil
				}
			}
		case "checkbox":
			_, valid = value.(bool)
		case "choice":
			multiple, _ := field.Config["multiple"].(bool)
			options, _ := field.Config["options"].([]any)
			if multiple {
				list, ok := value.([]any)
				valid = ok && (!field.Required || len(list) > 0)
				seen := map[string]bool{}
				for _, entry := range list {
					text, ok := entry.(string)
					if !ok || seen[text] || !containsOption(options, text) {
						valid = false
					}
					seen[text] = true
				}
			} else if text, ok := value.(string); ok {
				valid = containsOption(options, text)
			}
		case "linked_product", "staff_role":
			text, ok := value.(string)
			valid = ok && validUUID(text)
		case "linked_charges":
			switch typed := value.(type) {
			case string:
				valid = validUUID(typed)
			case []any:
				valid = !field.Required || len(typed) > 0
				seen := map[string]bool{}
				for _, entry := range typed {
					text, ok := entry.(string)
					if !ok || !validUUID(text) || seen[text] {
						valid = false
					}
					seen[text] = true
				}
			}
		}
		if !valid {
			errorsByField[key] = "invalid value"
		}
	}
	return errorsByField
}

func numericValue(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0)
}

func containsOption(options []any, value string) bool {
	for _, option := range options {
		if option == value {
			return true
		}
	}
	return false
}

func ValidateFormPatch(fields []DynamicField, merged, submitted map[string]any) map[string]string {
	enabledValues := map[string]any{}
	definitions := map[string]DynamicField{}
	for _, field := range fields {
		definitions[field.Key] = field
		if field.Enabled {
			if value, ok := merged[field.Key]; ok {
				enabledValues[field.Key] = value
			}
		}
	}
	validation := ValidateFormData(fields, enabledValues)
	for key := range submitted {
		field, known := definitions[key]
		if !known {
			validation[key] = "unknown field"
		} else if !field.Enabled {
			validation[key] = "field is disabled"
		}
	}
	return validation
}
