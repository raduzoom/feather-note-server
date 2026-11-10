//go:build integration

package api

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"feathernote/server/internal/auth"
	"feathernote/server/internal/config"
)

type syncer struct {
	e       *env
	token   string
	library string
}

func (e *env) syncer(email string) *syncer {
	k := newKeys(email)
	token, _ := e.account(k)
	return &syncer{e, token, k.library}
}

func change(id, typ string, base int, deleted bool, meta []byte, blob any) map[string]any {
	var m any
	if meta != nil {
		m = b64(meta)
	}
	return map[string]any{"id": id, "type": typ, "base_version": base, "deleted": deleted, "meta": m, "blob_sha256": blob}
}

func (s *syncer) push(changes ...map[string]any) response {
	return s.e.do(request{method: "POST", path: "/v1/sync/push", token: s.token, body: map[string]any{"library_id": s.library, "changes": changes}})
}

// one pushes a single change and returns its result.
func (s *syncer) one(c map[string]any) map[string]any {
	s.e.t.Helper()
	r := s.push(c)
	if r.status != 200 {
		s.e.t.Fatalf("push %d %s", r.status, r.body)
	}
	return r.json(s.e.t)["results"].([]any)[0].(map[string]any)
}

func (s *syncer) blob(size int) string {
	data := rnd(size)
	if r := s.e.put(s.token, data); r.status != 200 {
		s.e.t.Fatalf("blob %d %s", r.status, r.body)
	}
	return sha(data)
}

func rejected(t *testing.T, res map[string]any, code string) {
	t.Helper()
	if res["status"] != "rejected" || res["error"].(map[string]any)["code"] != code {
		t.Fatalf("want rejected %s, got %v", code, res)
	}
}

func TestPushRule1Create(t *testing.T) {
	e := newEnv(t)
	s := e.syncer("ana@example.com")
	id := auth.NewID()
	res := s.one(change(id, "tag", 0, false, rnd(20), nil))
	if res["status"] != "accepted" || res["version"] != float64(1) || res["seq"] != float64(1) || res["id"] != id {
		t.Fatalf("%v", res)
	}
	again := s.one(change(id, "tag", 0, false, rnd(20), nil))
	cur := again["current"].(map[string]any)
	if again["status"] != "conflict" || cur["version"] != float64(1) || cur["type"] != "tag" || cur["blob"] != nil || cur["deleted"] != false {
		t.Fatalf("%v", again)
	}
}

func TestPushRule2Update(t *testing.T) {
	e := newEnv(t)
	s := e.syncer("ana@example.com")
	id := auth.NewID()
	rejected(t, s.one(change(id, "tag", 3, false, rnd(5), nil)), "not_found")
	s.one(change(id, "tag", 0, false, rnd(5), nil))
	if res := s.one(change(id, "tag", 1, false, rnd(5), nil)); res["status"] != "accepted" || res["version"] != float64(2) {
		t.Fatalf("%v", res)
	}
	if res := s.one(change(id, "tag", 1, false, rnd(5), nil)); res["status"] != "conflict" || res["current"].(map[string]any)["version"] != float64(2) {
		t.Fatalf("%v", res)
	}
}

func TestPushRule3TypeMismatch(t *testing.T) {
	e := newEnv(t)
	s := e.syncer("ana@example.com")
	id := auth.NewID()
	s.one(change(id, "tag", 0, false, rnd(5), nil))
	rejected(t, s.one(change(id, "notebook", 1, false, rnd(5), nil)), "type_mismatch")
}

func TestPushRule4Tombstones(t *testing.T) {
	e := newEnv(t)
	s := e.syncer("ana@example.com")
	id := auth.NewID()
	blob := s.blob(100)
	s.one(change(id, "note", 0, false, rnd(5), blob))
	if res := s.one(change(id, "note", 1, true, nil, nil)); res["status"] != "accepted" || res["version"] != float64(2) {
		t.Fatalf("delete %v", res)
	}
	rejected(t, s.one(change(id, "note", 2, true, rnd(5), nil)), "invalid_request")
	if res := s.one(change(id, "note", 2, false, rnd(5), blob)); res["status"] != "accepted" || res["version"] != float64(3) {
		t.Fatalf("revive %v", res)
	}
}

func TestPushRule5Blobs(t *testing.T) {
	e := newEnv(t)
	s := e.syncer("ana@example.com")
	rejected(t, s.one(change(auth.NewID(), "note", 0, false, rnd(5), nil)), "invalid_request")
	rejected(t, s.one(change(auth.NewID(), "tag", 0, false, rnd(5), sha([]byte("x")))), "invalid_request")
	rejected(t, s.one(change(auth.NewID(), "attachment", 0, false, rnd(5), sha([]byte("never uploaded")))), "blob_missing")
	other := e.syncer("bo@example.com")
	theirs := other.blob(50)
	rejected(t, s.one(change(auth.NewID(), "note", 0, false, rnd(5), theirs)), "blob_missing")
	if res := s.one(change(auth.NewID(), "note", 0, false, rnd(5), s.blob(50))); res["status"] != "accepted" {
		t.Fatalf("%v", res)
	}
}

func TestPushRule6Quota(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.BillingEnabled = false; c.QuotaBytes = 100000 })
	s := e.syncer("ana@example.com")
	s.blob(10000)
	r := s.push(change(auth.NewID(), "tag", 0, false, rnd(40000), nil), change(auth.NewID(), "tag", 0, false, rnd(40000), nil),
		change(auth.NewID(), "tag", 0, false, rnd(40000), nil))
	results := r.json(t)["results"].([]any)
	if results[0].(map[string]any)["status"] != "accepted" || results[1].(map[string]any)["status"] != "accepted" {
		t.Fatalf("%v", results)
	}
	rejected(t, results[2].(map[string]any), "quota_exceeded")
	// deleting stays possible over quota
	id := results[0].(map[string]any)["id"].(string)
	if res := s.one(change(id, "tag", 1, true, nil, nil)); res["status"] != "accepted" {
		t.Fatalf("delete over quota %v", res)
	}
}

func TestPushRule7Accepted(t *testing.T) {
	e := newEnv(t)
	s := e.syncer("ana@example.com")
	a, b := auth.NewID(), auth.NewID()
	r := s.push(change(a, "tag", 0, false, rnd(5), nil), change(b, "notebook", 0, false, rnd(5), nil))
	res := r.json(t)["results"].([]any)
	if res[0].(map[string]any)["seq"] != float64(1) || res[1].(map[string]any)["seq"] != float64(2) || res[1].(map[string]any)["id"] != b {
		t.Fatalf("%v", res)
	}
	items := s.pull("", 0).json(t)["items"].([]any)
	if items[0].(map[string]any)["updated_at"] != "2026-11-09T14:32:00.123Z" {
		t.Fatalf("%v", items[0])
	}
}

func TestPushWholeRequestErrors(t *testing.T) {
	e := newEnv(t)
	s := e.syncer("ana@example.com")
	wrong := &syncer{e, s.token, auth.NewID()}
	if r := wrong.push(change(auth.NewID(), "tag", 0, false, rnd(5), nil)); r.status != 400 || r.code() != "library_mismatch" {
		t.Fatalf("library %d %s", r.status, r.body)
	}
	many := make([]map[string]any, 101)
	for i := range many {
		many[i] = change(auth.NewID(), "tag", 0, false, rnd(5), nil)
	}
	if r := s.push(many...); r.status != 400 || r.code() != "invalid_request" {
		t.Fatalf("101 %d %s", r.status, r.body)
	}
	_, _ = e.db.Exec(context.Background(), "UPDATE accounts SET plan_state = 'read_only', plan_reason = 'trial_expired'")
	if r := s.push(change(auth.NewID(), "tag", 0, false, rnd(5), nil)); r.status != 403 || r.code() != "read_only" {
		t.Fatalf("read only %d %s", r.status, r.body)
	}
	if r := s.pull("", 0); r.status != 200 {
		t.Fatalf("pull while read only %d", r.status)
	}
}

func TestPushPerChangeValidation(t *testing.T) {
	e := newEnv(t)
	s := e.syncer("ana@example.com")
	good := auth.NewID()
	// a change whose id isn't a ULID can't get a result of its own (results carry the id): the request is refused
	if r := s.push(change("not-a-ulid", "tag", 0, false, rnd(5), nil), change(auth.NewID(), "tag", 0, false, rnd(5), nil)); r.status != 400 || r.code() != "invalid_request" {
		t.Fatalf("bad id: %d %s", r.status, r.body)
	}
	r := s.push(
		change(auth.NewID(), "tag", 0, false, rnd(5), nil),
		change(auth.NewID(), "folder", 0, false, rnd(5), nil),
		change(auth.NewID(), "tag", 0, false, rnd(65537), nil),
		change(auth.NewID(), "tag", 0, false, nil, nil),
		change(auth.NewID(), "note", 0, true, nil, sha([]byte("x"))),
		change(auth.NewID(), "tag", -1, false, rnd(5), nil),
		change(good, "tag", 0, false, rnd(5), nil),
	)
	results := r.json(t)["results"].([]any)
	if results[0].(map[string]any)["status"] != "accepted" {
		t.Fatalf("%v", results[0])
	}
	for i := 1; i < 6; i++ {
		rejected(t, results[i].(map[string]any), "invalid_request")
	}
	if results[6].(map[string]any)["status"] != "accepted" {
		t.Fatalf("%v", results[6])
	}
}

func TestConcurrentPushOneWins(t *testing.T) { // Review Focus 1
	e := newEnv(t)
	s := e.syncer("ana@example.com")
	id := auth.NewID()
	s.one(change(id, "tag", 0, false, rnd(5), nil))
	for v := 1; v < 4; v++ {
		s.one(change(id, "tag", v, false, rnd(5), nil))
	}
	var wg sync.WaitGroup
	results := make([]map[string]any, 10)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = s.one(change(id, "tag", 4, false, rnd(5), nil))
		}(i)
	}
	wg.Wait()
	accepted := 0
	for _, r := range results {
		switch r["status"] {
		case "accepted":
			accepted++
			if r["version"] != float64(5) {
				t.Fatalf("%v", r)
			}
		case "conflict":
			if r["current"].(map[string]any)["version"] != float64(5) {
				t.Fatalf("%v", r)
			}
		default:
			t.Fatalf("%v", r)
		}
	}
	var last int
	_ = e.db.QueryRow(context.Background(), "SELECT last_seq FROM accounts").Scan(&last)
	if accepted != 1 || last != 5 {
		t.Fatalf("accepted %d, last_seq %d", accepted, last)
	}
}

func (s *syncer) pull(cursor string, limit int) response {
	path := "/v1/sync/pull?library_id=" + s.library
	if cursor != "" {
		path += "&cursor=" + cursor
	}
	if limit != 0 {
		path += fmt.Sprintf("&limit=%d", limit)
	}
	return s.e.do(request{method: "GET", path: path, token: s.token})
}

func TestPullPagesInSeqOrder(t *testing.T) {
	e := newEnv(t)
	s := e.syncer("ana@example.com")
	ids := make([]string, 7)
	for i := range ids {
		ids[i] = auth.NewID()
		s.one(change(ids[i], "tag", 0, false, rnd(5), nil))
	}
	page := func(cursor string) (items []any, next string, more bool) {
		r := s.pull(cursor, 3)
		if r.status != 200 {
			t.Fatalf("pull %d %s", r.status, r.body)
		}
		m := r.json(t)
		return m["items"].([]any), m["cursor"].(string), m["has_more"].(bool)
	}
	p1, c1, more1 := page("")
	if len(p1) != 3 || !more1 || c1 != "c_000000000003" || p1[0].(map[string]any)["id"] != ids[0] {
		t.Fatalf("page 1: %d %s %v", len(p1), c1, more1)
	}
	s.one(change(ids[0], "tag", 1, true, nil, nil)) // ids[0] becomes a tombstone with seq 8
	p2, c2, more2 := page(c1)
	p3, c3, more3 := page(c2)
	if len(p2) != 3 || !more2 || len(p3) != 2 || more3 {
		t.Fatalf("pages %d %v %d %v", len(p2), more2, len(p3), more3)
	}
	last := p3[1].(map[string]any)
	if last["id"] != ids[0] || last["deleted"] != true || last["meta"] != nil || last["blob"] != nil || last["seq"] != float64(8) {
		t.Fatalf("tombstone %v", last)
	}
	empty, c4, more4 := page(c3)
	if len(empty) != 0 || more4 || c4 != c3 {
		t.Fatalf("after the end: %d %s %v", len(empty), c4, more4)
	}
	for _, bad := range []string{"limit=0", "limit=1001", "cursor=xyz", "limit=abc"} {
		if r := e.do(request{method: "GET", path: "/v1/sync/pull?library_id=" + s.library + "&" + bad, token: s.token}); r.status != 400 || r.code() != "invalid_request" {
			t.Errorf("%s: %d %s", bad, r.status, r.body)
		}
	}
	if r := e.do(request{method: "GET", path: "/v1/sync/pull?library_id=" + auth.NewID(), token: s.token}); r.code() != "library_mismatch" {
		t.Fatalf("library %s", r.body)
	}
	all := s.pull("", 0).json(t)["items"].([]any)
	if len(all) != 7 {
		t.Fatalf("default limit returned %d", len(all))
	}
}

func TestPullShowsBlobSize(t *testing.T) {
	e := newEnv(t)
	s := e.syncer("ana@example.com")
	blob := s.blob(321)
	s.one(change(auth.NewID(), "note", 0, false, rnd(5), blob))
	item := s.pull("", 0).json(t)["items"].([]any)[0].(map[string]any)
	if b := item["blob"].(map[string]any); b["sha256"] != blob || b["size"] != float64(321) {
		t.Fatalf("%v", item)
	}
}
