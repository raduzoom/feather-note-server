// Package auth implements accounts, sign-in, sessions and devices (sync-protocol.md "Account lifecycle").
package auth

import (
	"crypto/sha256"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var errBadEmail = errors.New("invalid email")

// NormalizeEmail trims, applies NFC and lowercases (crypto.md "Normalization"). Control characters, U+2028 and
// U+2029 make it invalid, so an address can't break a line in mail headers or the server's output.
func NormalizeEmail(s string) (string, error) {
	n := strings.ToLower(norm.NFC.String(strings.TrimSpace(s)))
	at := strings.IndexByte(n, '@')
	if c := utf8.RuneCountInString(n); c < 3 || c > 254 || at <= 0 || at == len(n)-1 || strings.Count(n, "@") != 1 ||
		strings.ContainsFunc(n, breaksLines) {
		return "", errBadEmail
	}
	return n, nil
}

func breaksLines(r rune) bool { return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' }

// EmailHash is what trial_records keeps after an account is deleted.
func EmailHash(norm string) []byte { return Hash([]byte(norm)) }

// Hash is SHA-256.
func Hash(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}
