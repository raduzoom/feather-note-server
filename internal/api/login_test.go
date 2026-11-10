//go:build integration

package api

import (
	"strings"
	"testing"
	"time"

	"feathernote/server/internal/auth"
)

func (e *env) login(k keys, device string) response {
	return e.do(request{method: "POST", path: "/v1/auth/login", body: map[string]any{"email": k.email, "auth_key": b64(k.authKey), "device_name": device}})
}

func TestLoginDoesNotRevealAccounts(t *testing.T) { // Review Focus 3
	e := newEnv(t)
	k := newKeys("ana@example.com")
	e.account(k)
	wrong := k
	wrong.authKey = rnd(32)
	unknown := newKeys("nobody@example.com")
	a, b := e.login(wrong, "Mac"), e.login(unknown, "Mac")
	if a.status != 401 || b.status != 401 || string(a.body) != string(b.body) || a.code() != "unauthorized" {
		t.Fatalf("%d %s / %d %s", a.status, a.body, b.status, b.body)
	}
	pending := newKeys("pending@example.com")
	e.do(request{method: "POST", path: "/v1/auth/signup", body: pending.signupBody()})
	if r := e.login(pending, "Mac"); r.status != 403 || r.code() != "email_unverified" {
		t.Fatalf("unverified: %d %s", r.status, r.body)
	}
	r := e.login(k, "iPhone")
	m := r.json(t)
	if r.status != 200 || m["wrapped_master_password"] != b64(k.wrappedPw) || m["account"].(map[string]any)["email"] != "ana@example.com" || len(m["device_id"].(string)) != 26 {
		t.Fatalf("login %d %s", r.status, r.body)
	}
}

func TestPreloginFakeSaltIsStable(t *testing.T) { // Review Focus 3
	e := newEnv(t)
	k := newKeys("ana@example.com")
	e.account(k)
	pre := func(email string) map[string]any {
		r := e.do(request{method: "POST", path: "/v1/auth/prelogin", body: map[string]any{"email": email}})
		if r.status != 200 {
			t.Fatalf("prelogin %d %s", r.status, r.body)
		}
		return r.json(t)
	}
	u1, u2, known := pre("Nobody@Example.com "), pre("nobody@example.com"), pre("ana@example.com")
	if u1["salt"] != u2["salt"] || u1["salt"] != b64(auth.FakeSalt(e.cfg.ServerSecret, "nobody@example.com")) {
		t.Fatalf("fake salt %v %v", u1, u2)
	}
	if known["salt"] != b64(k.salt) {
		t.Fatalf("known salt %v", known)
	}
	for _, m := range []map[string]any{u1, known} {
		kdf := m["kdf"].(map[string]any)
		if len(m) != 2 || kdf["alg"] != "argon2id13" || kdf["t"] != float64(3) || kdf["m_kib"] != float64(262144) || kdf["p"] != float64(1) {
			t.Fatalf("shape %v", m)
		}
	}
}

func TestRecoveryScopeIsConfined(t *testing.T) { // Review Focus 4
	e := newEnv(t)
	k := newKeys("ana@example.com")
	e.account(k)
	r := e.do(request{method: "POST", path: "/v1/auth/recover", body: map[string]any{"email": k.email, "recovery_auth_key": b64(k.recoveryAuthKey), "device_name": "Mac"}})
	m := r.json(t)
	if r.status != 200 || m["wrapped_master_recovery"] != b64(k.wrappedRecov) {
		t.Fatalf("recover %d %s", r.status, r.body)
	}
	token := m["token"].(string)
	checked := 0
	for _, route := range Routes() {
		method, path, _ := strings.Cut(route, " ")
		if routeIsPublic(route) || route == "POST /account/password" || route == "POST /auth/logout" {
			continue
		}
		path = strings.ReplaceAll(path, "{device_id}", auth.NewID())
		path = strings.ReplaceAll(path, "{sha256}", strings.Repeat("a", 64))
		if res := e.do(request{method: method, path: "/v1" + path, token: token}); res.status != 403 || res.code() != "forbidden_scope" {
			t.Errorf("%s: %d %s", route, res.status, res.body)
		}
		checked++
	}
	if checked < 2 {
		t.Fatalf("only %d routes checked", checked)
	}
	if res := e.do(request{method: "POST", path: "/v1/auth/logout", token: token}); res.status != 204 {
		t.Fatalf("logout %d %s", res.status, res.body)
	}
	if bad := e.do(request{method: "POST", path: "/v1/auth/recover", body: map[string]any{"email": k.email, "recovery_auth_key": b64(rnd(32)), "device_name": "Mac"}}); bad.status != 401 {
		t.Fatalf("wrong recovery key %d", bad.status)
	}
}

func TestSessionsExpireAfter90Days(t *testing.T) {
	e := newEnv(t)
	a, _ := e.account(newKeys("ana@example.com"))
	b, _ := e.account(newKeys("bo@example.com"))
	devices := func(token string) int { return e.do(request{method: "GET", path: "/v1/devices", token: token}).status }
	e.advance(89 * 24 * time.Hour)
	if devices(b) != 200 {
		t.Fatal("day 89")
	}
	e.advance(24*time.Hour + time.Second) // a: 90 days + 1 s idle; b: 1 day
	if devices(a) != 401 {
		t.Fatal("a should have expired")
	}
	e.advance(88 * 24 * time.Hour)
	if devices(b) != 200 {
		t.Fatal("b used at day 89 should still work at day 178")
	}
}

func TestDevices(t *testing.T) {
	e := newEnv(t)
	k := newKeys("ana@example.com")
	first, _ := e.account(k)
	second := e.login(k, "iPhone").json(t)
	r := e.do(request{method: "GET", path: "/v1/devices", token: first})
	list := r.json(t)["devices"].([]any)
	if r.status != 200 || len(list) != 2 {
		t.Fatalf("devices %d %s", r.status, r.body)
	}
	currents := 0
	for _, d := range list {
		m := d.(map[string]any)
		if m["current"] == true {
			currents++
			if m["name"] != "Mac" {
				t.Fatalf("current %v", m)
			}
		}
		if m["created_at"] == nil || m["last_seen_at"] == nil || len(m["id"].(string)) != 26 {
			t.Fatalf("device %v", m)
		}
	}
	if currents != 1 {
		t.Fatalf("%d current", currents)
	}
	if res := e.do(request{method: "DELETE", path: "/v1/devices/" + second["device_id"].(string), token: first}); res.status != 204 {
		t.Fatalf("revoke %d %s", res.status, res.body)
	}
	if res := e.do(request{method: "GET", path: "/v1/devices", token: second["token"].(string)}); res.status != 401 {
		t.Fatalf("revoked token %d", res.status)
	}
	other, _ := e.account(newKeys("bo@example.com"))
	otherDevice := e.do(request{method: "GET", path: "/v1/devices", token: other}).json(t)["devices"].([]any)[0].(map[string]any)["id"].(string)
	if res := e.do(request{method: "DELETE", path: "/v1/devices/" + otherDevice, token: first}); res.status != 404 || res.code() != "not_found" {
		t.Fatalf("other account's device %d %s", res.status, res.body)
	}
	if res := e.do(request{method: "POST", path: "/v1/auth/logout", token: first}); res.status != 204 {
		t.Fatalf("logout %d", res.status)
	}
	if res := e.do(request{method: "GET", path: "/v1/devices", token: first}); res.status != 401 || res.code() != "unauthorized" {
		t.Fatalf("after logout %d", res.status)
	}
	if res := e.do(request{method: "GET", path: "/v1/devices"}); res.status != 401 {
		t.Fatalf("no token %d", res.status)
	}
	if res := e.do(request{method: "GET", path: "/v1/devices", token: "fn1_" + strings.Repeat("A", 43)}); res.status != 401 {
		t.Fatalf("unknown token %d", res.status)
	}
}

func TestAuthRateLimits(t *testing.T) {
	e := newEnv(t)
	k := newKeys("ana@example.com")
	for i := 0; i < 10; i++ {
		e.login(k, "Mac")
	}
	if r := e.login(k, "Mac"); r.status != 429 || r.header.Get("Retry-After") == "" {
		t.Fatalf("11th login per email: %d", r.status)
	}
	for i := 0; i < 10; i++ {
		e.do(request{method: "POST", path: "/v1/auth/prelogin", ip: "8.8.8.8", body: map[string]any{"email": auth.NewID() + "@x.io"}})
	}
	if r := e.do(request{method: "POST", path: "/v1/auth/prelogin", ip: "8.8.8.8", body: map[string]any{"email": "z@x.io"}}); r.status != 429 {
		t.Fatalf("11th prelogin per IP: %d", r.status)
	}
}
