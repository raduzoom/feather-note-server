// Package storetest gives each integration test a fresh, migrated Postgres database.
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"feathernote/server/internal/store"
)

// NewDB creates database test_<random> next to FEATHER_TEST_DATABASE_URL's, migrates it, and drops it when
// the test ends. Without the variable the test fails: integration tests must never pass without a database.
func NewDB(t testing.TB) *pgxpool.Pool {
	t.Helper()
	admin := os.Getenv("FEATHER_TEST_DATABASE_URL")
	if admin == "" {
		t.Fatal("FEATHER_TEST_DATABASE_URL is not set; run the integration tests with deploy/docker-compose.test.yml")
	}
	ctx := context.Background()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "test_" + hex.EncodeToString(b)
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(admin)
	u.Path = "/" + name
	db, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_, _ = conn.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		conn.Close(context.Background())
	})
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}
