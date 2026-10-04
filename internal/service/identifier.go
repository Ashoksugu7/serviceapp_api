package service

import (
	"strings"
)

// LoginIdentifier is the normalized email or phone a user signs in with.
// Exactly one field is set.
type LoginIdentifier struct {
	Email string
	Phone string
}

func (l LoginIdentifier) key() string {
	if l.Phone != "" {
		return "phone:" + l.Phone
	}
	return l.Email
}

// ParseLoginIdentifier treats input containing "@" as an email and anything
// else as a phone number.
func ParseLoginIdentifier(raw string) (LoginIdentifier, error) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "@") {
		email, err := NormalizeEmail(raw)
		return LoginIdentifier{Email: email}, err
	}
	phone, err := NormalizePhone(raw)
	return LoginIdentifier{Phone: phone}, err
}

// NormalizePhone keeps only digits so formatting never creates a second login.
// It accepts digits, spaces, "+", "-", "." and parentheses and needs 6-20 digits.
func NormalizePhone(raw string) (string, error) {
	var digits strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case strings.ContainsRune(" +-.()", r):
		default:
			return "", ErrValidation
		}
	}
	if n := digits.Len(); n < 6 || n > 20 {
		return "", ErrValidation
	}
	return digits.String(), nil
}
