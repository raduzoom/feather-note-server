//go:build integration

package plans

import (
	"context"
	"testing"
	"time"

	"feathernote/server/internal/store/storetest"
)

func TestUsedBytes(t *testing.T) {
	db := storetest.NewDB(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO accounts (id, email_norm, salt, kdf, auth_hash, recovery_auth_hash, wrapped_master_password, wrapped_master_recovery, library_id, plan_state, created_at)
	      VALUES ('A', 'a@x.io', '', '{}', '', '', '', '', 'L', 'trial', $1)`, now)
	item := func(id string, seq int, deleted bool, meta []byte, blob any) {
		exec(`INSERT INTO items (account_id, id, type, version, seq, deleted, meta, blob_sha256, updated_at) VALUES ('A', $1, 'note', 1, $2, $3, $4, $5, $6)`, id, seq, deleted, meta, blob, now)
	}
	item("i1", 1, false, make([]byte, 100), "aa")
	item("i2", 2, false, make([]byte, 50), "aa")
	item("i3", 3, true, nil, nil)
	blob := func(sha string, size int, age time.Duration) {
		exec(`INSERT INTO blobs (account_id, sha256, size, uploaded_at) VALUES ('A', $1, $2, $3)`, sha, size, now.Add(-age))
	}
	blob("aa", 1000, 30*24*time.Hour)
	blob("bb", 500, time.Hour)
	blob("cc", 700, 25*time.Hour)
	used, err := UsedBytes(ctx, db, "A", now)
	if err != nil || used != 1650 {
		t.Fatalf("used %d %v", used, err)
	}
}
