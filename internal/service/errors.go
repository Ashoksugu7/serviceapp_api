package service

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// reasonError keeps an error kind for errors.Is checks and adds a sentence
// that is safe to show to API clients. Reasons never include submitted values.
type reasonError struct {
	kind   error
	reason string
}

func (e *reasonError) Error() string { return e.kind.Error() + ": " + e.reason }
func (e *reasonError) Unwrap() error { return e.kind }

// InvalidState rejects an operation that conflicts with current data (HTTP 409).
func InvalidState(reason string) error { return &reasonError{kind: ErrInvalidState, reason: reason} }

// Invalid rejects a malformed request (HTTP 400).
func Invalid(reason string) error { return &reasonError{kind: ErrValidation, reason: reason} }

// Forbidden rejects an operation the caller's role may not perform (HTTP 403).
func Forbidden(reason string) error { return &reasonError{kind: ErrForbidden, reason: reason} }

// Message returns the client-facing explanation carried by err, or fallback.
// Errors wrapped as fmt.Errorf("%w: detail", kind) are also supported.
func Message(err error, fallback string) string {
	var reasoned *reasonError
	if errors.As(err, &reasoned) {
		return reasoned.reason
	}
	text := err.Error()
	for _, kind := range []error{ErrValidation, ErrInvalidState, ErrForbidden} {
		if detail, found := strings.CutPrefix(text, kind.Error()+": "); found && detail != "" {
			return sentence(detail)
		}
	}
	return fallback
}

func sentence(text string) string {
	first, size := utf8.DecodeRuneInString(text)
	text = string(unicode.ToUpper(first)) + text[size:]
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	return text
}
