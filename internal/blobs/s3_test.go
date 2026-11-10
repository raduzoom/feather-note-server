//go:build integration

package blobs_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"feathernote/server/internal/blobs/blobstest"
)

func TestS3RoundTrip(t *testing.T) {
	s, _ := blobstest.NewStorage(t)
	ctx := context.Background()
	if err := s.Put(ctx, "A/one", bytes.NewReader([]byte("hello")), 5); err != nil {
		t.Fatal(err)
	}
	_ = s.Put(ctx, "B/two", bytes.NewReader([]byte("x")), 1)
	r, size, err := s.Get(ctx, "A/one")
	if err != nil || size != 5 {
		t.Fatalf("%v %d", err, size)
	}
	data, _ := io.ReadAll(r)
	r.Close()
	if string(data) != "hello" {
		t.Fatalf("%q", data)
	}
	keys, err := s.List(ctx, "A/")
	if err != nil || len(keys) != 1 || keys[0] != "A/one" {
		t.Fatalf("%v %v", keys, err)
	}
	if err := s.Delete(ctx, "A/one"); err != nil {
		t.Fatal(err)
	}
	if keys, _ := s.List(ctx, "A/"); len(keys) != 0 {
		t.Fatalf("after delete %v", keys)
	}
}
