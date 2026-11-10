//go:build integration

package api

import (
	"context"
	"testing"
	"time"

	"feathernote/server/internal/plans"
)

func TestTrialExpires(t *testing.T) {
	e := newEnv(t)
	k := newKeys("ana@example.com")
	token, _ := e.account(k)
	account := func() map[string]any {
		r := e.do(request{method: "GET", path: "/v1/account", token: token})
		if r.status != 200 {
			t.Fatalf("account %d %s", r.status, r.body)
		}
		return r.json(t)["plan"].(map[string]any)
	}
	if p := account(); p["state"] != "trial" {
		t.Fatalf("%v", p)
	}
	e.advance(14*24*time.Hour + time.Millisecond)
	if p := account(); p["state"] != "read_only" || p["reason"] != "trial_expired" || p["quota_bytes"] != float64(1073741824) {
		t.Fatalf("expired %v", p)
	}
	var id string
	_ = e.db.QueryRow(context.Background(), "SELECT id FROM accounts").Scan(&id)
	if err := plans.SetState(context.Background(), e.db, id, "active", nil); err != nil {
		t.Fatal(err)
	}
	if p := account(); p["state"] != "active" || p["reason"] != nil || p["quota_bytes"] != float64(10737418240) {
		t.Fatalf("active %v", p)
	}
}

func (e *env) setPassword(token string, current []byte, k *keys) response {
	k.salt, k.authKey, k.wrappedPw = rnd(16), rnd(32), rnd(74)
	body := map[string]any{"salt": b64(k.salt), "kdf": v1KDF, "auth_key": b64(k.authKey), "wrapped_master_password": b64(k.wrappedPw)}
	if current != nil {
		body["current_auth_key"] = b64(current)
	}
	return e.do(request{method: "POST", path: "/v1/account/password", token: token, body: body})
}

func TestPasswordChange(t *testing.T) {
	e := newEnv(t)
	k := newKeys("ana@example.com")
	mac, _ := e.account(k)
	phone := e.login(k, "iPhone").json(t)["token"].(string)
	old := k
	next := k
	if r := e.setPassword(mac, rnd(32), &next); r.status != 401 {
		t.Fatalf("wrong current key: %d %s", r.status, r.body)
	}
	next = k
	r := e.setPassword(mac, k.authKey, &next)
	if r.status != 200 {
		t.Fatalf("change %d %s", r.status, r.body)
	}
	newToken := r.json(t)["token"].(string)
	for _, tok := range []string{mac, phone} {
		if res := e.do(request{method: "GET", path: "/v1/account", token: tok}); res.status != 401 {
			t.Fatalf("old session still works: %d", res.status)
		}
	}
	if res := e.do(request{method: "GET", path: "/v1/account", token: newToken}); res.status != 200 {
		t.Fatalf("new token %d", res.status)
	}
	if res := e.login(old, "Mac"); res.status != 401 {
		t.Fatalf("old key %d", res.status)
	}
	if res := e.login(next, "Mac"); res.status != 200 {
		t.Fatalf("new key %d", res.status)
	}
	pre := e.do(request{method: "POST", path: "/v1/auth/prelogin", body: map[string]any{"email": k.email}}).json(t)
	if pre["salt"] != b64(next.salt) {
		t.Fatal("prelogin salt not updated")
	}
	var devices int
	_ = e.db.QueryRow(context.Background(), "SELECT count(*) FROM devices").Scan(&devices)
	if devices != 2 { // the Mac kept its device; the phone's is gone; the last login added one
		t.Fatalf("%d devices", devices)
	}
}

func TestRecoverThenSetPassword(t *testing.T) {
	e := newEnv(t)
	k := newKeys("ana@example.com")
	mac, _ := e.account(k)
	rec := e.do(request{method: "POST", path: "/v1/auth/recover", body: map[string]any{"email": k.email, "recovery_auth_key": b64(k.recoveryAuthKey), "device_name": "Mac"}}).json(t)["token"].(string)
	full := k
	if r := e.setPassword(mac, nil, &full); r.status != 400 || r.code() != "invalid_request" {
		t.Fatalf("full scope without current key: %d %s", r.status, r.body)
	}
	next := k
	r := e.setPassword(rec, nil, &next)
	if r.status != 200 {
		t.Fatalf("recovery set %d %s", r.status, r.body)
	}
	newToken := r.json(t)["token"].(string)
	for _, tok := range []string{mac, rec} {
		if res := e.do(request{method: "POST", path: "/v1/auth/logout", token: tok}); res.status != 401 {
			t.Fatalf("earlier session %d", res.status)
		}
	}
	if res := e.do(request{method: "GET", path: "/v1/account", token: newToken}); res.status != 200 {
		t.Fatalf("new token isn't full scope: %d %s", res.status, res.body)
	}
	if res := e.login(next, "Mac"); res.status != 200 {
		t.Fatal("new password")
	}
}
