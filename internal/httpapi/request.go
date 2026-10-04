package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
)

const maxBodyBytes int64 = 1 << 20

// decodeJSON is the shared body boundary for future business handlers.
// Callers pass a pointer to a typed request struct, then validate business rules.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Content-Type must be application/json")
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "Request body exceeds 1 MiB")
		} else {
			writeError(w, http.StatusBadRequest, "INVALID_JSON", "Could not read request body")
		}
		return false
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "Request body must be a JSON object")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		// Decoder errors identify the malformed JSON token or unknown field and
		// are safe to return/log: they never include the request body itself.
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON object: "+err.Error())
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "Request body must contain exactly one JSON object")
		return false
	}
	return true
}
