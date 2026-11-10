//go:build integration

package store_test

import (
	"context"
	"testing"

	"feathernote/server/internal/store"
	"feathernote/server/internal/store/storetest"
)

func TestMigrateIsIdempotent(t *testing.T) {
	db := storetest.NewDB(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, db); err != nil { // NewDB already migrated once
		t.Fatal(err)
	}
	var versions []int
	rows, err := db.Query(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var v int
		_ = rows.Scan(&v)
		versions = append(versions, v)
	}
	if len(versions) != 1 || versions[0] != 1 {
		t.Fatalf("versions %v", versions)
	}
	for _, table := range []string{"accounts", "pending_signups", "trial_records", "devices", "sessions", "items", "blobs"} {
		var ok bool
		if err := db.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&ok); err != nil || !ok {
			t.Errorf("table %s missing (%v)", table, err)
		}
	}
}
