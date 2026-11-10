// Package blobstest gives each integration test a bucket of its own on the compose SeaweedFS.
package blobstest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"feathernote/server/internal/blobs"
	"feathernote/server/internal/config"
)

// NewStorage creates bucket test-<random> and empties and removes it when the test ends.
func NewStorage(t testing.TB) (blobs.Storage, config.S3Config) {
	t.Helper()
	endpoint := os.Getenv("FEATHER_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Fatal("FEATHER_TEST_S3_ENDPOINT is not set; run the integration tests with deploy/docker-compose.test.yml")
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	cfg := config.S3Config{Endpoint: endpoint, Region: "us-east-1", Bucket: "test-" + hex.EncodeToString(b),
		AccessKey: os.Getenv("FEATHER_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("FEATHER_TEST_S3_SECRET_KEY")}
	s, err := blobs.NewS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		keys, _ := s.List(ctx, "")
		for _, k := range keys {
			_ = s.Delete(ctx, k)
		}
		_ = s.(interface{ RemoveBucket(context.Context) error }).RemoveBucket(ctx)
	})
	return s, cfg
}
