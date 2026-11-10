// Package httpx holds the JSON, error and encoding conventions of sync-protocol.md.
package httpx

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// Error is a protocol error: HTTP status, stable code, English message for logs.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func newErr(status int, code, msg string) *Error { return &Error{status, code, msg} }

// The protocol's errors (sync-protocol.md "Errors").
var (
	ErrUnauthorized        = newErr(401, "unauthorized", "Unauthorized.")
	ErrForbiddenScope      = newErr(403, "forbidden_scope", "This session may only set a new password or sign out.")
	ErrEmailUnverified     = newErr(403, "email_unverified", "The email address is not verified.")
	ErrVerificationInvalid = newErr(400, "verification_invalid", "Wrong code, or too many attempts.")
	ErrVerificationExpired = newErr(400, "verification_expired", "The code has expired.")
	ErrRateLimited         = newErr(429, "rate_limited", "Too many requests.")
	ErrReadOnly            = newErr(403, "read_only", "The account is read-only.")
	ErrQuotaExceeded       = newErr(403, "quota_exceeded", "Storage quota exceeded.")
	ErrLibraryMismatch     = newErr(400, "library_mismatch", "Wrong library.")
	ErrBlobHashMismatch    = newErr(400, "blob_hash_mismatch", "The blob does not match its SHA-256.")
	ErrBlobTooLarge        = newErr(413, "blob_too_large", "The blob is too large.")
	ErrNotFound            = newErr(404, "not_found", "Not found.")
	ErrServer              = newErr(500, "server_error", "Internal error.")
)

// InvalidRequest is a 400 for malformed JSON or invalid fields.
func InvalidRequest(msg string) *Error { return newErr(400, "invalid_request", msg) }

// WriteJSON writes v with the protocol's content type.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes a protocol error; anything that isn't one becomes 500 server_error, its text never sent.
func WriteError(w http.ResponseWriter, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = ErrServer
	}
	type body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	WriteJSON(w, e.Status, map[string]body{"error": {e.Code, e.Message}})
}

// Decode reads a JSON body of at most maxBytes. Unknown fields are allowed (newer clients may add some).
func Decode(r *http.Request, v any, maxBytes int64) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		return InvalidRequest("Unreadable body.")
	}
	if int64(len(data)) > maxBytes {
		return InvalidRequest("Body too large.")
	}
	if len(data) == 0 {
		return InvalidRequest("Empty body.")
	}
	if err := json.Unmarshal(data, v); err != nil {
		return InvalidRequest("Malformed JSON.")
	}
	return nil
}

// B64 is binary data as standard base64 with padding.
type B64 []byte

func (b B64) MarshalJSON() ([]byte, error) { return json.Marshal(base64.StdEncoding.EncodeToString(b)) }

func (b *B64) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	v, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	*b = v
	return nil
}

// Time formats a server timestamp: RFC 3339, UTC, milliseconds.
func Time(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }
