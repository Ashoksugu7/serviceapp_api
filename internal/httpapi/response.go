package httpapi

import (
	"encoding/json"
	"net/http"
)

type errorDetail struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeFieldError(w, status, code, message, nil)
}

func writeFieldError(w http.ResponseWriter, status int, code, message string, fields map[string]string) {
	writeJSON(w, status, struct {
		Error errorDetail `json:"error"`
	}{Error: errorDetail{Code: code, Message: message, Fields: fields}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		data = []byte(`{"error":{"code":"INTERNAL_ERROR","message":"Internal server error"}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}
