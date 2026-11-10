//go:build integration

package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"feathernote/server/internal/auth"
	"feathernote/server/internal/janitor"
)

func (e *env) janitor() *janitor.Janitor {
	return &janitor.Janitor{DB: e.db, Storage: e.storage, Clock: e.clock, Log: e.deps().Log}
}

func (e *env) count(sql string, args ...any) int {
	var n int
	if err := e.db.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestDeletedAccountIsLockedOutAndWiped(t *testing.T) { // Review Focus 5
	e := newEnv(t)
	e.account(newKeys("ana@example.com")) // another account, which must stay
	k := newKeys("bo@example.com")
	token, _ := e.account(k)
	s := &syncer{e, token, k.library}
	authKey := k.authKey
	phone := e.login(k, "iPhone").json(t)["token"].(string)
	accountID := ""
	_ = e.db.QueryRow(context.Background(), "SELECT id FROM accounts WHERE email_norm = 'bo@example.com'").Scan(&accountID)
	s.one(change(auth.NewID(), "note", 0, false, rnd(10), s.blob(200)))
	s.blob(300)
	if r := e.do(request{method: "DELETE", path: "/v1/account", token: s.token, body: map[string]any{"auth_key": b64(rnd(32))}}); r.status != 401 {
		t.Fatalf("wrong key %d", r.status)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); e.put(phone, rnd(100)) }() // races the delete: either lands or is refused
	if r := e.do(request{method: "DELETE", path: "/v1/account", token: s.token, body: map[string]any{"auth_key": b64(authKey)}}); r.status != 202 {
		t.Fatalf("delete %d %s", r.status, r.body)
	}
	wg.Wait()
	for _, tok := range []string{s.token, phone} {
		if r := e.do(request{method: "GET", path: "/v1/account", token: tok}); r.status != 401 {
			t.Fatalf("token after delete %d", r.status)
		}
	}
	if r := e.login(keys{email: "bo@example.com", authKey: authKey}, "Mac"); r.status != 401 {
		t.Fatalf("login after delete %d", r.status)
	}
	if err := e.janitor().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if keys, _ := e.storage.List(context.Background(), accountID+"/"); len(keys) != 0 {
		t.Fatalf("objects left %v", keys)
	}
	if e.count("SELECT count(*) FROM accounts WHERE id = $1", accountID)+e.count("SELECT count(*) FROM items WHERE account_id = $1", accountID)+
		e.count("SELECT count(*) FROM blobs WHERE account_id = $1", accountID) != 0 {
		t.Fatal("rows left")
	}
	if e.count("SELECT count(*) FROM trial_records WHERE email_hash = $1", auth.EmailHash("bo@example.com")) != 1 {
		t.Fatal("trial record gone")
	}
	if e.count("SELECT count(*) FROM accounts WHERE email_norm = 'ana@example.com'") != 1 {
		t.Fatal("another account was touched")
	}
	_, m := e.account(newKeys("bo@example.com"))
	if m["account"].(map[string]any)["plan"].(map[string]any)["reason"] != "trial_already_used" {
		t.Fatalf("second trial %v", m)
	}
}

func TestJanitorOrphanBlobs(t *testing.T) {
	e := newEnv(t)
	s := e.syncer("ana@example.com")
	ctx := context.Background()
	kept := s.blob(100)
	s.one(change(auth.NewID(), "note", 0, false, rnd(5), kept))
	orphan := s.blob(100)
	deletedID := auth.NewID()
	onceUsed := s.blob(100)
	s.one(change(deletedID, "note", 0, false, rnd(5), onceUsed))
	s.one(change(deletedID, "note", 1, true, nil, nil))
	e.advance(23 * time.Hour)
	_ = e.janitor().Run(ctx)
	if e.count("SELECT count(*) FROM blobs") != 3 {
		t.Fatal("young orphans removed")
	}
	e.advance(2 * time.Hour)
	_ = e.janitor().Run(ctx)
	objects, _ := e.storage.List(ctx, "")
	if e.count("SELECT count(*) FROM blobs") != 1 || len(objects) != 1 || objects[0] != e.accountID()+"/"+kept {
		t.Fatalf("after 25 h: %d rows, objects %v (orphan %s, once used %s)", e.count("SELECT count(*) FROM blobs"), objects, orphan, onceUsed)
	}
	e.advance(30 * 24 * time.Hour)
	_ = e.janitor().Run(ctx)
	if e.count("SELECT count(*) FROM blobs") != 1 {
		t.Fatal("referenced blob removed")
	}
}

func TestJanitorExpiry(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	old, _ := e.account(newKeys("ana@example.com"))
	e.do(request{method: "POST", path: "/v1/auth/signup", body: newKeys("pending@example.com").signupBody()})
	e.advance(25 * time.Hour)
	e.do(request{method: "POST", path: "/v1/auth/signup", body: newKeys("fresh@example.com").signupBody()})
	fresh, _ := e.account(newKeys("bo@example.com"))
	_ = e.janitor().Run(ctx)
	if e.count("SELECT count(*) FROM pending_signups WHERE email_norm = 'pending@example.com'") != 0 ||
		e.count("SELECT count(*) FROM pending_signups WHERE email_norm = 'fresh@example.com'") != 1 {
		t.Fatal("pending signups")
	}
	e.advance(90*24*time.Hour - 25*time.Hour + time.Second) // ana idle 90 days + 1 s; bo 90 days - 25 h
	_ = e.janitor().Run(ctx)
	if e.count("SELECT count(*) FROM devices") != 1 {
		t.Fatalf("%d devices", e.count("SELECT count(*) FROM devices"))
	}
	if r := e.do(request{method: "GET", path: "/v1/devices", token: fresh}); r.status != 200 {
		t.Fatal("fresh session removed")
	}
	_ = old
}

// Review I1: a client whose push failed re-uploads its blob a day later, then pushes. The re-upload must keep
// the blob from the janitor, or the note would point at a deleted object.
func TestReuploadKeepsAnOldBlob(t *testing.T) {
	e := newEnv(t)
	s := e.syncer("ana@example.com")
	ctx := context.Background()
	data := rnd(200)
	if r := e.put(s.token, data); r.status != 200 {
		t.Fatal(r.status)
	}
	e.advance(25 * time.Hour)
	if r := e.put(s.token, data); r.status != 200 { // the retry before the push
		t.Fatal(r.status)
	}
	if err := e.janitor().Run(ctx); err != nil {
		t.Fatal(err)
	}
	if res := s.one(change(auth.NewID(), "note", 0, false, rnd(5), sha(data))); res["status"] != "accepted" {
		t.Fatalf("push %v", res)
	}
	if r := e.do(request{method: "GET", path: "/v1/blobs/" + sha(data), token: s.token}); r.status != 200 || string(r.body) != string(data) {
		t.Fatalf("download after push: %d", r.status)
	}
}
