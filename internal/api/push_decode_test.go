package api

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"feathernote/server/internal/items"
)

// W-F2: a push body of millions of tiny changes is never turned into millions of structs.
func TestPushChangesStopAfterTheLimit(t *testing.T) {
	body := `{"library_id":"x","changes":[` + strings.Repeat("{},", 1_000_000) + `{}]}`
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	var req pushRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if len(req.Changes) != items.MaxChanges+1 {
		t.Fatalf("%d changes decoded", len(req.Changes))
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 4<<20 {
		t.Fatalf("decoding allocated %d bytes", grew)
	}
	for _, c := range []string{`{"library_id":"x"}`, `{"library_id":"x","changes":null}`} {
		var r pushRequest
		if err := json.Unmarshal([]byte(c), &r); err != nil || r.Changes != nil {
			t.Fatalf("%s: %v %v", c, r.Changes, err)
		}
	}
	var empty pushRequest
	if err := json.Unmarshal([]byte(`{"changes":[]}`), &empty); err != nil || empty.Changes == nil || len(empty.Changes) != 0 {
		t.Fatalf("empty list: %v %v", empty.Changes, err)
	}
	var bad pushRequest
	if err := json.Unmarshal([]byte(`{"changes":{}}`), &bad); err == nil {
		t.Fatal("an object for changes was accepted")
	}
}
