// Package blobs stores encrypted note bodies and attachments: objects in S3 at <account_id>/<sha256>, and a
// row per blob for ownership, size and quota (sync-protocol.md "Blobs").
package blobs

import (
	"context"
	"io"
)

// Storage is the object store.
type Storage interface {
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
	EnsureBucket(ctx context.Context) error
}

// Key is a blob's object name. Blobs are never shared between accounts.
func Key(accountID, sha string) string { return accountID + "/" + sha }
