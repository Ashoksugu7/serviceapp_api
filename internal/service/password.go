package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 19 * 1024
	argonIterations  = 2
	argonParallelism = 1
	argonSaltLength  = 16
	argonKeyLength   = 32
)

var commonPasswords = map[string]struct{}{
	"123456789012345": {}, "admin123456789": {}, "letmein123456789": {},
	"password123456": {}, "qwerty123456789": {}, "serviceops360": {},
	"password12": {}, "password123": {}, "password1234": {}, "password12345": {},
	"qwerty1234": {}, "qwerty12345": {}, "admin12345": {}, "admin123456": {},
	"welcome123": {}, "letmein123": {}, "1q2w3e4r5t": {}, "abcd123456": {},
}

// ValidateNewPassword accepts 10-128 characters containing at least one letter
// and one number (T26), and rejects packaged common passwords. Passwords are
// never trimmed or normalized; spaces and Unicode are allowed.
func ValidateNewPassword(password string) error {
	length := utf8.RuneCountInString(password)
	if length < 10 || length > 128 {
		return fmt.Errorf("%w: password must contain 10 to 128 characters", ErrValidation)
	}
	hasLetter, hasDigit := false, false
	for _, r := range password {
		hasLetter = hasLetter || unicode.IsLetter(r)
		hasDigit = hasDigit || unicode.IsDigit(r)
	}
	if !hasLetter || !hasDigit {
		return fmt.Errorf("%w: password must include at least one letter and one number", ErrValidation)
	}
	if _, found := commonPasswords[strings.ToLower(strings.TrimSpace(password))]; found {
		return fmt.Errorf("%w: password is too common", ErrValidation)
	}
	return nil
}

func HashPassword(password string) (string, error) {
	if err := ValidateNewPassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonIterations,
		argonParallelism, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var memory uint64
	var iterations uint64
	var parallelism uint64
	for _, setting := range strings.Split(parts[3], ",") {
		keyValue := strings.SplitN(setting, "=", 2)
		if len(keyValue) != 2 {
			return false
		}
		value, err := strconv.ParseUint(keyValue[1], 10, 32)
		if err != nil {
			return false
		}
		switch keyValue[0] {
		case "m":
			memory = value
		case "t":
			iterations = value
		case "p":
			parallelism = value
		default:
			return false
		}
	}
	if memory != argonMemory || iterations != argonIterations || parallelism != argonParallelism {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != argonSaltLength {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != argonKeyLength {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, uint32(iterations), uint32(memory), uint8(parallelism), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// temporaryAlphabet leaves out look-alike characters (0/O, 1/l/I) so a
// temporary password can be read out or retyped from an email.
const temporaryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"

// GenerateTemporaryPassword returns a random 12-character password that meets
// ValidateNewPassword (T28). Users must replace it at first sign-in.
func GenerateTemporaryPassword() (string, error) {
	for {
		raw := make([]byte, 12)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		password := make([]byte, len(raw))
		for i, b := range raw {
			password[i] = temporaryAlphabet[int(b)%len(temporaryAlphabet)]
		}
		if ValidateNewPassword(string(password)) == nil {
			return string(password), nil
		}
	}
}
