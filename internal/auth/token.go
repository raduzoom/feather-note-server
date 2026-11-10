package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"

	"github.com/oklog/ulid/v2"
)

const tokenPrefix = "fn1_"

// NewToken is "fn1_" + 32 random bytes in base64url without padding, and the SHA-256 the server stores.
func NewToken() (token string, hash []byte) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token = tokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return token, Hash([]byte(token))
}

// HashToken returns the stored form of a token, or false if it isn't shaped like one.
func HashToken(token string) ([]byte, bool) {
	rest, ok := strings.CutPrefix(token, tokenPrefix)
	if !ok || len(rest) != 43 {
		return nil, false
	}
	if _, err := base64.RawURLEncoding.DecodeString(rest); err != nil {
		return nil, false
	}
	return Hash([]byte(token)), true
}

// NewCode is a 6-digit verification code and its hash.
func NewCode() (code string, hash []byte) {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	code = fmt.Sprintf("%06d", n.Int64())
	return code, Hash([]byte(code))
}

// Equal compares in constant time.
func Equal(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }

// FakeSalt is what prelogin returns for an email with no account (crypto.md "Authentication with the server").
func FakeSalt(secret []byte, norm string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(norm))
	return m.Sum(nil)[:16]
}

// NewID is a new ULID in text form.
func NewID() string { return ulid.Make().String() }
