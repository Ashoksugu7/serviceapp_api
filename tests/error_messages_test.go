package tests

import (
	"errors"
	"fmt"
	"testing"

	"serviceops360/api/internal/service"
)

func TestErrorMessagesExplainTheRejection(t *testing.T) {
	cases := []struct {
		name string
		err  error
		kind error
		want string
	}{
		{"reasoned invalid state", service.InvalidState("The prefix cannot change after the first record has been created."), service.ErrInvalidState, "The prefix cannot change after the first record has been created."},
		{"reasoned validation", service.Invalid("page_size must be a whole number from 1 to 100."), service.ErrValidation, "page_size must be a whole number from 1 to 100."},
		{"reasoned forbidden", service.Forbidden("Only an admin can reopen a closed record."), service.ErrForbidden, "Only an admin can reopen a closed record."},
		{"wrapped detail becomes a sentence", fmt.Errorf("%w: invalid formula", service.ErrValidation), service.ErrValidation, "Invalid formula."},
		{"bare kind uses the fallback", service.ErrInvalidState, service.ErrInvalidState, "fallback"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, tc.kind) {
				t.Fatalf("errors.Is(%v, %v) = false", tc.err, tc.kind)
			}
			if got := service.Message(tc.err, "fallback"); got != tc.want {
				t.Fatalf("Message() = %q, want %q", got, tc.want)
			}
		})
	}
}
