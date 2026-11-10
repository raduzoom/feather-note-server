// Package auth implements accounts, sign-in, sessions and devices (sync-protocol.md "Account lifecycle").
package auth

import (
	"crypto/sha256"
	"errors"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var errBadEmail = errors.New("invalid email")

// NormalizeEmail trims, applies NFC and lowercases (crypto.md "Normalization").
func NormalizeEmail(s string) (string, error) {
	n := strings.ToLower(norm.NFC.String(strings.TrimSpace(s)))
	at := strings.IndexByte(n, '@')
	if c := utf8.RuneCountInString(n); c < 3 || c > 254 || at <= 0 || at == len(n)-1 || strings.Count(n, "@") != 1 {
		return "", errBadEmail
	}
	return n, nil
}

// EmailHash is what trial_records keeps after an account is deleted.
func EmailHash(norm string) []byte { return Hash([]byte(norm)) }

// Hash is SHA-256.
func Hash(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}
