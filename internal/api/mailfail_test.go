//go:build integration

package api

import (
	"strings"
	"testing"
)

// Review Focus 2: the email provider failing; then recovering
func TestSMTPFailureThenResendDelivers(t *testing.T) {
	e := newEnv(t)
	k := newKeys("ana@example.com")
	e.mail.FailNext(1)
	r := e.do(request{method: "POST", path: "/v1/auth/signup", body: k.signupBody()})
	if r.status != 500 || r.code() != "server_error" || strings.Contains(strings.ToLower(string(r.body)), "smtp") {
		t.Fatalf("signup while mail fails: %d %s", r.status, r.body)
	}
	if r := e.do(request{method: "POST", path: "/v1/auth/resend", body: map[string]any{"email": k.email}}); r.status != 202 {
		t.Fatalf("resend %d", r.status)
	}
	code := e.mail.LastCode(k.email)
	if code == "" {
		t.Fatal("no code after the provider recovered")
	}
	if r := e.do(request{method: "POST", path: "/v1/auth/verify", body: map[string]any{"email": k.email, "code": code, "device_name": "Mac"}}); r.status != 200 {
		t.Fatalf("verify %d %s", r.status, r.body)
	}
}
