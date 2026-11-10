//go:build integration

package api

import (
	"context"
	"regexp"
	"testing"
	"time"

	"feathernote/server/internal/config"
	"feathernote/server/internal/httpx"
)

func TestSignupVerifyCreatesTrialAccount(t *testing.T) {
	e := newEnv(t)
	k := newKeys("Ana@Example.com")
	token, m := e.account(k)
	if !regexp.MustCompile(`^fn1_[A-Za-z0-9_-]{43}$`).MatchString(token) || len(m["device_id"].(string)) != 26 {
		t.Fatalf("%v", m)
	}
	acct := m["account"].(map[string]any)
	plan := acct["plan"].(map[string]any)
	if acct["email"] != "ana@example.com" || acct["library_id"] != k.library || acct["billing_enabled"] != true {
		t.Fatalf("account %v", acct)
	}
	if plan["state"] != "trial" || plan["reason"] != nil || plan["trial_ends_at"] != httpx.Time(e.clock().Add(14*24*time.Hour)) ||
		plan["quota_bytes"] != float64(1073741824) || plan["used_bytes"] != float64(0) {
		t.Fatalf("plan %v", plan)
	}
}

func TestSignupDoesNotRevealAccounts(t *testing.T) { // Review Focus 3
	e := newEnv(t)
	k := newKeys("ana@example.com")
	e.account(k)
	codeBefore := e.mail.LastCode("ana@example.com")
	again := e.do(request{method: "POST", path: "/v1/auth/signup", body: newKeys("ana@example.com").signupBody()})
	fresh := e.do(request{method: "POST", path: "/v1/auth/signup", body: newKeys("bo@example.com").signupBody()})
	if again.status != 202 || fresh.status != 202 || string(again.body) != string(fresh.body) ||
		again.header.Get("Content-Type") != fresh.header.Get("Content-Type") {
		t.Fatalf("differ: %d %q / %d %q", again.status, again.body, fresh.status, fresh.body)
	}
	if e.mail.Notices("ana@example.com") != 1 || e.mail.LastCode("ana@example.com") != codeBefore {
		t.Fatal("expected one notice and no new code")
	}
	var auth []byte
	_ = e.db.QueryRow(context.Background(), "SELECT auth_hash FROM accounts WHERE email_norm='ana@example.com'").Scan(&auth)
	if string(auth) != string(hashOf(k.authKey)) {
		t.Fatal("existing account changed")
	}
}

func TestSignupValidation(t *testing.T) {
	e := newEnv(t)
	bad := []func(m map[string]any){
		func(m map[string]any) { m["salt"] = b64(rnd(15)) },
		func(m map[string]any) { m["auth_key"] = b64(rnd(31)) },
		func(m map[string]any) { m["wrapped_master_password"] = b64(rnd(73)) },
		func(m map[string]any) { m["library_id"] = "not-a-ulid" },
		func(m map[string]any) {
			m["kdf"] = map[string]any{"alg": "argon2id13", "t": 3, "m_kib": 262144, "p": 2}
		},
		func(m map[string]any) { m["email"] = "nobody" },
		func(m map[string]any) { m["salt"] = "***" },
	}
	for i, f := range bad {
		body := newKeys("ana@example.com").signupBody()
		f(body)
		if r := e.do(request{method: "POST", path: "/v1/auth/signup", body: body}); r.status != 400 || r.code() != "invalid_request" {
			t.Errorf("case %d: %d %s", i, r.status, r.body)
		}
	}
}

func TestVerifyCodeRules(t *testing.T) {
	e := newEnv(t)
	k := newKeys("ana@example.com")
	verify := func(code string) response {
		return e.do(request{method: "POST", path: "/v1/auth/verify", body: map[string]any{"email": k.email, "code": code, "device_name": "Mac"}})
	}
	wrong := func(c string) string {
		if c == "000000" {
			return "000001"
		}
		return "000000"
	}
	e.do(request{method: "POST", path: "/v1/auth/signup", body: k.signupBody()})
	code := e.mail.LastCode(k.email)
	for i := 0; i < 5; i++ {
		if r := verify(wrong(code)); r.code() != "verification_invalid" {
			t.Fatalf("try %d: %s", i, r.body)
		}
	}
	if r := verify(code); r.code() != "verification_invalid" {
		t.Fatalf("6th try with the right code: %s", r.body)
	}
	// a new signup replaces the old one
	e.do(request{method: "POST", path: "/v1/auth/signup", body: k.signupBody()})
	code2 := e.mail.LastCode(k.email)
	if code2 != code {
		if r := verify(code); r.code() != "verification_invalid" {
			t.Fatalf("old code after a new signup: %s", r.body)
		}
	}
	e.advance(15*time.Minute + time.Second)
	if r := verify(code2); r.status != 400 || r.code() != "verification_expired" {
		t.Fatalf("expired: %d %s", r.status, r.body)
	}
	if r := e.do(request{method: "POST", path: "/v1/auth/verify", body: map[string]any{"email": "nobody@example.com", "code": "123456", "device_name": "Mac"}}); r.code() != "verification_expired" {
		t.Fatalf("no pending: %s", r.body)
	}
	// resend within the day works; after 24 h the pending signup is gone
	if r := e.do(request{method: "POST", path: "/v1/auth/resend", body: map[string]any{"email": k.email}}); r.status != 202 {
		t.Fatalf("resend %d", r.status)
	}
	code3 := e.mail.LastCode(k.email)
	e.advance(24 * time.Hour)
	if r := verify(code3); r.code() != "verification_expired" {
		t.Fatalf("24 h: %s", r.body)
	}
	if r := verify("12345"); r.status != 400 || r.code() != "invalid_request" {
		t.Fatalf("5 digits: %s", r.body)
	}
}

func TestSecondTrialIsReadOnly(t *testing.T) {
	e := newEnv(t)
	e.account(newKeys("ana@example.com"))
	if _, err := e.db.Exec(context.Background(), "DELETE FROM accounts"); err != nil {
		t.Fatal(err)
	}
	_, m := e.account(newKeys("ana@example.com"))
	plan := m["account"].(map[string]any)["plan"].(map[string]any)
	if plan["state"] != "read_only" || plan["reason"] != "trial_already_used" || plan["trial_ends_at"] != nil {
		t.Fatalf("plan %v", plan)
	}
}

func TestBillingOffIsActive(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.BillingEnabled = false; c.QuotaBytes = 5000 })
	_, m := e.account(newKeys("ana@example.com"))
	acct := m["account"].(map[string]any)
	plan := acct["plan"].(map[string]any)
	if plan["state"] != "active" || plan["trial_ends_at"] != nil || plan["quota_bytes"] != float64(5000) || acct["billing_enabled"] != false {
		t.Fatalf("plan %v", acct)
	}
}

func TestSignupAndResendRateLimits(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		r := e.do(request{method: "POST", path: "/v1/auth/signup", ip: "9.9.9.9", body: newKeys("a@example.com").signupBody()})
		if r.status != 202 || r.header.Get("RateLimit-Limit") != "5" || r.header.Get("RateLimit-Remaining") == "" || r.header.Get("RateLimit-Reset") == "" {
			t.Fatalf("signup %d: %d %v", i, r.status, r.header)
		}
	}
	r := e.do(request{method: "POST", path: "/v1/auth/signup", ip: "9.9.9.9", body: newKeys("a@example.com").signupBody()})
	if r.status != 429 || r.code() != "rate_limited" || r.header.Get("Retry-After") == "" {
		t.Fatalf("6th signup: %d %s", r.status, r.body)
	}
	for i := 0; i < 3; i++ {
		if r := e.do(request{method: "POST", path: "/v1/auth/resend", body: map[string]any{"email": "a@example.com"}}); r.status != 202 {
			t.Fatalf("resend %d: %d", i, r.status)
		}
	}
	if r := e.do(request{method: "POST", path: "/v1/auth/resend", body: map[string]any{"email": "A@example.com"}}); r.status != 429 {
		t.Fatalf("4th resend: %d", r.status)
	}
}
