//go:build integration

package api

import (
	"context"
	"fmt"
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
		return e.do(request{method: "POST", path: "/v1/auth/verify", body: k.verifyBody(code)})
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
	if r := e.do(request{method: "POST", path: "/v1/auth/verify", body: newKeys("nobody@example.com").verifyBody("123456")}); r.code() != "verification_expired" {
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
		if r := e.do(request{method: "POST", path: "/v1/auth/resend", body: map[string]any{"email": "b@example.com"}}); r.status != 202 {
			t.Fatalf("resend %d: %d", i, r.status)
		}
	}
	if r := e.do(request{method: "POST", path: "/v1/auth/resend", body: map[string]any{"email": "B@example.com"}}); r.status != 429 {
		t.Fatalf("4th resend: %d", r.status)
	}
	// addresses in one IPv6 /64 share the per-IP limit
	for i := 0; i < 5; i++ {
		ip := fmt.Sprintf("2001:db8:5:6::%x", i+1)
		if r := e.do(request{method: "POST", path: "/v1/auth/signup", ip: ip, body: newKeys(fmt.Sprintf("v6-%d@example.com", i)).signupBody()}); r.status != 202 {
			t.Fatalf("v6 signup %d: %d", i, r.status)
		}
	}
	if r := e.do(request{method: "POST", path: "/v1/auth/signup", ip: "2001:db8:5:6:ffff::9", body: newKeys("v6-x@example.com").signupBody()}); r.status != 429 {
		t.Fatalf("6th signup from the same /64: %d", r.status)
	}
}

// S-F3: a stranger who replaces a pending signup can block it, but the code alone never creates an account.
func TestVerifyNeedsTheSignupsAuthKey(t *testing.T) {
	e := newEnv(t)
	victim, attacker := newKeys("ana@example.com"), newKeys("ana@example.com")
	e.do(request{method: "POST", path: "/v1/auth/signup", body: victim.signupBody()})
	e.do(request{method: "POST", path: "/v1/auth/signup", body: attacker.signupBody()})
	code := e.mail.LastCode("ana@example.com") // the newer code, which reached the victim's inbox
	if r := e.do(request{method: "POST", path: "/v1/auth/verify", body: victim.verifyBody(code)}); r.status != 400 || r.code() != "verification_invalid" {
		t.Fatalf("victim's key against the replaced signup: %d %s", r.status, r.body)
	}
	if e.count("SELECT count(*) FROM accounts") != 0 {
		t.Fatal("an account was created with someone else's keys")
	}
	body := victim.verifyBody(code)
	delete(body, "auth_key")
	if r := e.do(request{method: "POST", path: "/v1/auth/verify", body: body}); r.status != 400 || r.code() != "invalid_request" {
		t.Fatalf("no auth_key: %d %s", r.status, r.body)
	}
	// the victim signs up again and gets in with their own keys
	e.do(request{method: "POST", path: "/v1/auth/signup", body: victim.signupBody()})
	if r := e.do(request{method: "POST", path: "/v1/auth/verify", body: victim.verifyBody(e.mail.LastCode("ana@example.com"))}); r.status != 200 {
		t.Fatalf("victim's own signup: %d %s", r.status, r.body)
	}
	if r := e.login(attacker, "Mac"); r.status != 401 {
		t.Fatalf("attacker's keys sign in: %d %s", r.status, r.body)
	}
}

// S-F4, S-F7: new signups and resends don't refill the guesses; 20 failed checks per email per 24 hours.
func TestVerifyFailureBudgetSurvivesNewSignups(t *testing.T) {
	e := newEnv(t)
	k := newKeys("ana@example.com")
	wrong := func(c string) string {
		if c == "000000" {
			return "000001"
		}
		return "000000"
	}
	for round := 0; round < 4; round++ {
		e.do(request{method: "POST", path: "/v1/auth/signup", body: k.signupBody()})
		code := e.mail.LastCode(k.email)
		for i := 0; i < 5; i++ {
			if r := e.do(request{method: "POST", path: "/v1/auth/verify", body: k.verifyBody(wrong(code))}); r.code() != "verification_invalid" {
				t.Fatalf("round %d try %d: %s", round, i, r.body)
			}
		}
		e.advance(time.Hour)
	}
	e.do(request{method: "POST", path: "/v1/auth/signup", body: k.signupBody()})
	if r := e.do(request{method: "POST", path: "/v1/auth/verify", body: k.verifyBody(e.mail.LastCode(k.email))}); r.code() != "verification_invalid" {
		t.Fatalf("right code after 20 failures: %d %s", r.status, r.body)
	}
	e.advance(24 * time.Hour)
	e.do(request{method: "POST", path: "/v1/auth/signup", body: k.signupBody()})
	if r := e.do(request{method: "POST", path: "/v1/auth/verify", body: k.verifyBody(e.mail.LastCode(k.email))}); r.status != 200 {
		t.Fatalf("a day later: %d %s", r.status, r.body)
	}
}

func TestVerifyRateLimitPerIP(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 10; i++ {
		r := e.do(request{method: "POST", path: "/v1/auth/verify", ip: "9.9.9.9", body: newKeys(fmt.Sprintf("n%d@example.com", i)).verifyBody("123456")})
		if r.code() != "verification_expired" || r.header.Get("RateLimit-Limit") != "10" {
			t.Fatalf("verify %d: %d %s %v", i, r.status, r.body, r.header)
		}
	}
	if r := e.do(request{method: "POST", path: "/v1/auth/verify", ip: "9.9.9.9", body: newKeys("n@example.com").verifyBody("123456")}); r.status != 429 {
		t.Fatalf("11th verify: %d", r.status)
	}
}

// Signup and resend together send at most 5 mails an hour per email, whether or not it has an account.
func TestCodesPerEmailLimit(t *testing.T) {
	e := newEnv(t)
	e.account(newKeys("taken@example.com")) // one signup counted already
	for _, email := range []string{"fresh@example.com", "taken@example.com"} {
		sent := 0
		if email == "taken@example.com" {
			sent = 1
		}
		for ; sent < 3; sent++ {
			if r := e.do(request{method: "POST", path: "/v1/auth/signup", body: newKeys(email).signupBody()}); r.status != 202 {
				t.Fatalf("%s signup %d: %d", email, sent, r.status)
			}
		}
		for ; sent < 5; sent++ {
			if r := e.do(request{method: "POST", path: "/v1/auth/resend", body: map[string]any{"email": email}}); r.status != 202 {
				t.Fatalf("%s resend %d: %d", email, sent, r.status)
			}
		}
		if r := e.do(request{method: "POST", path: "/v1/auth/signup", body: newKeys(email).signupBody()}); r.status != 429 {
			t.Fatalf("%s 6th mail: %d", email, r.status)
		}
	}
	e.advance(time.Hour)
	if r := e.do(request{method: "POST", path: "/v1/auth/signup", body: newKeys("fresh@example.com").signupBody()}); r.status != 202 {
		t.Fatalf("an hour later: %d", r.status)
	}
}

// S-F1, S-F5: after a signup, login, verify and resend answer the same for an email with an account as for one
// without, and the account itself never changes.
func TestFollowUpsAfterSignupDoNotRevealAccounts(t *testing.T) {
	e := newEnv(t)
	owner := newKeys("ana@example.com")
	e.account(owner)
	codeBefore := e.mail.LastCode("ana@example.com")
	taken, fresh := newKeys("ana@example.com"), newKeys("bo@example.com")
	for _, k := range []keys{taken, fresh} {
		if r := e.do(request{method: "POST", path: "/v1/auth/signup", body: k.signupBody()}); r.status != 202 {
			t.Fatalf("signup %s: %d", k.email, r.status)
		}
	}
	same := func(step string, a, b response) response {
		t.Helper()
		if a.status != b.status || string(a.body) != string(b.body) {
			t.Fatalf("%s differs: %d %s / %d %s", step, a.status, a.body, b.status, b.body)
		}
		return a
	}
	notCode := func(k keys) string {
		if e.mail.LastCode(k.email) == "000000" {
			return "000001"
		}
		return "000000"
	}
	verify := func(k keys, code string) response {
		return e.do(request{method: "POST", path: "/v1/auth/verify", body: k.verifyBody(code)})
	}
	if r := same("login with the signup's key", e.login(taken, "Mac"), e.login(fresh, "Mac")); r.code() != "email_unverified" {
		t.Fatalf("login: %s", r.body)
	}
	if r := same("wrong code", verify(taken, notCode(taken)), verify(fresh, notCode(fresh))); r.code() != "verification_invalid" {
		t.Fatalf("wrong code: %s", r.body)
	}
	other := func(k keys) keys { k.authKey = rnd(32); return k }
	same("someone else's key", verify(other(taken), notCode(taken)), verify(other(fresh), notCode(fresh)))
	resend := func(k keys) response {
		return e.do(request{method: "POST", path: "/v1/auth/resend", body: map[string]any{"email": k.email}})
	}
	freshCode := e.mail.LastCode(fresh.email)
	same("resend", resend(taken), resend(fresh))
	if e.mail.Notices("ana@example.com") != 2 || e.mail.LastCode("ana@example.com") != codeBefore || e.mail.LastCode(fresh.email) == freshCode {
		t.Fatal("resend: expected a second notice and no code for ana, a new code for bo")
	}
	e.advance(15*time.Minute + time.Second)
	if r := same("expired code", verify(taken, notCode(taken)), verify(fresh, e.mail.LastCode(fresh.email))); r.code() != "verification_expired" {
		t.Fatalf("expired: %s", r.body)
	}
	// the account is untouched: still one, still the owner's keys
	if e.count("SELECT count(*) FROM accounts WHERE email_norm = 'ana@example.com'") != 1 || e.mail.LastCode("ana@example.com") != codeBefore {
		t.Fatal("the account or its mail changed")
	}
	if r := e.login(owner, "iPhone"); r.status != 200 {
		t.Fatalf("owner's login: %d %s", r.status, r.body)
	}
}

// S-F2: resend has a per-IP limit ahead of the per-email one.
func TestResendRateLimitPerIP(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 10; i++ {
		if r := e.do(request{method: "POST", path: "/v1/auth/resend", ip: "9.9.9.9", body: map[string]any{"email": fmt.Sprintf("r%d@example.com", i)}}); r.status != 202 {
			t.Fatalf("resend %d: %d", i, r.status)
		}
	}
	if r := e.do(request{method: "POST", path: "/v1/auth/resend", ip: "9.9.9.9", body: map[string]any{"email": "r@example.com"}}); r.status != 429 {
		t.Fatalf("11th resend from one IP: %d", r.status)
	}
}
