// Package blobstest gives each integration test its own corner of one shared test bucket on the compose SeaweedFS.
// One bucket, not one per test: SeaweedFS reserves volumes per bucket, and a bucket per test ran out of them.
package blobstest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"

	"feathernote/server/internal/blobs"
	"feathernote/server/internal/config"
)

// prefixed keeps a test's objects under its own prefix, so listings see only that test's keys.
type prefixed struct {
	inner  blobs.Storage
	prefix string
}

func (p prefixed) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	return p.inner.Put(ctx, p.prefix+key, r, size)
}

func (p prefixed) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	return p.inner.Get(ctx, p.prefix+key)
}

func (p prefixed) Delete(ctx context.Context, key string) error {
	return p.inner.Delete(ctx, p.prefix+key)
}

func (p prefixed) EnsureBucket(ctx context.Context) error { return p.inner.EnsureBucket(ctx) }

func (p prefixed) List(ctx context.Context, prefix string) ([]string, error) {
	keys, err := p.inner.List(ctx, p.prefix+prefix)
	for i, k := range keys {
		keys[i] = strings.TrimPrefix(k, p.prefix)
	}
	return keys, err
}

// NewStorage returns storage in the shared bucket "feather-test" under a prefix of its own, emptied when the
// test ends.
func NewStorage(t testing.TB) (blobs.Storage, config.S3Config) {
	t.Helper()
	endpoint := os.Getenv("FEATHER_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Fatal("FEATHER_TEST_S3_ENDPOINT is not set; run the integration tests with deploy/docker-compose.test.yml")
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	cfg := config.S3Config{Endpoint: endpoint, Region: "us-east-1", Bucket: "feather-test",
		AccessKey: os.Getenv("FEATHER_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("FEATHER_TEST_S3_SECRET_KEY")}
	inner, err := blobs.NewS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := inner.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	s := prefixed{inner: inner, prefix: "t-" + hex.EncodeToString(b) + "/"}
	t.Cleanup(func() {
		keys, _ := s.List(ctx, "")
		for _, k := range keys {
			_ = s.Delete(ctx, k)
		}
	})
	return s, cfg
}
