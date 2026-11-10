//go:build integration

package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"testing"

	"feathernote/server/internal/blobs"
	"feathernote/server/internal/config"
	"feathernote/server/internal/plans"
)

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (e *env) put(token string, data []byte) response {
	return e.do(request{method: "PUT", path: "/v1/blobs/" + sha(data), token: token, body: data})
}

func (e *env) accountID() string {
	var id string
	_ = e.db.QueryRow(context.Background(), "SELECT id FROM accounts WHERE deleted_at IS NULL ORDER BY created_at LIMIT 1").Scan(&id)
	return id
}

func TestBlobUpload(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.MaxBlobBytes = 1024 })
	token, _ := e.account(newKeys("ana@example.com"))
	data := rnd(500)
	r := e.put(token, data)
	m := r.json(t)
	if r.status != 200 || m["sha256"] != sha(data) || m["size"] != float64(500) {
		t.Fatalf("put %d %s", r.status, r.body)
	}
	if r := e.put(token, data); r.status != 200 {
		t.Fatalf("again %d", r.status)
	}
	if keys, _ := e.storage.List(context.Background(), e.accountID()+"/"); len(keys) != 1 || keys[0] != blobs.Key(e.accountID(), sha(data)) {
		t.Fatalf("objects %v", keys)
	}
	if r := e.do(request{method: "PUT", path: "/v1/blobs/" + sha(rnd(3)), token: token, body: data}); r.status != 400 || r.code() != "blob_hash_mismatch" {
		t.Fatalf("mismatch %d %s", r.status, r.body)
	}
	if r := e.do(request{method: "PUT", path: "/v1/blobs/ABC", token: token, body: data}); r.status != 400 || r.code() != "invalid_request" {
		t.Fatalf("bad name %d %s", r.status, r.body)
	}
	if r := e.put(token, rnd(1025)); r.status != 413 || r.code() != "blob_too_large" {
		t.Fatalf("too large %d %s", r.status, r.body)
	}
	if err := plans.SetState(context.Background(), e.db, e.accountID(), "active", nil); err != nil {
		t.Fatal(err)
	}
	_, _ = e.db.Exec(context.Background(), "UPDATE accounts SET plan_state = 'read_only', plan_reason = 'subscription_ended'")
	if r := e.put(token, rnd(10)); r.status != 403 || r.code() != "read_only" {
		t.Fatalf("read only %d %s", r.status, r.body)
	}
}

func TestBlobQuota(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.BillingEnabled = false; c.QuotaBytes = 1000 })
	token, _ := e.account(newKeys("ana@example.com"))
	if r := e.put(token, rnd(600)); r.status != 200 {
		t.Fatalf("first %d", r.status)
	}
	if r := e.put(token, rnd(600)); r.status != 403 || r.code() != "quota_exceeded" {
		t.Fatalf("over quota %d %s", r.status, r.body)
	}
}

func TestBlobDownloadIsPerAccount(t *testing.T) {
	e := newEnv(t)
	a, _ := e.account(newKeys("ana@example.com"))
	b, _ := e.account(newKeys("bo@example.com"))
	data := rnd(300)
	e.put(a, data)
	r := e.do(request{method: "GET", path: "/v1/blobs/" + sha(data), token: a})
	if r.status != 200 || string(r.body) != string(data) || r.header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("owner %d %v", r.status, r.header)
	}
	if r := e.do(request{method: "GET", path: "/v1/blobs/" + sha(data), token: b}); r.status != 404 || r.code() != "not_found" {
		t.Fatalf("other account %d %s", r.status, r.body)
	}
	if r := e.put(b, data); r.status != 200 {
		t.Fatalf("b upload %d", r.status)
	}
	all, _ := e.storage.List(context.Background(), "")
	if len(all) != 2 {
		t.Fatalf("expected one object per account, got %v", all)
	}
}

func TestParallelUploadsRespectQuota(t *testing.T) { // Review Focus 2
	e := newEnv(t, func(c *config.Config) { c.BillingEnabled = false; c.QuotaBytes = 3500 })
	token, _ := e.account(newKeys("ana@example.com"))
	var wg sync.WaitGroup
	statuses := make([]int, 5)
	for i := range statuses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i] = e.put(token, rnd(1000)).status
		}(i)
	}
	wg.Wait()
	ok, refused := 0, 0
	for _, s := range statuses {
		switch s {
		case 200:
			ok++
		case 403:
			refused++
		}
	}
	used, _ := plans.UsedBytes(context.Background(), e.db, e.accountID(), e.clock())
	if ok != 3 || refused != 2 || used > 3500 {
		t.Fatalf("ok %d refused %d used %d (%v)", ok, refused, used, statuses)
	}
}
