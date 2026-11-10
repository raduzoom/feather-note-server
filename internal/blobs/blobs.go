package blobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"feathernote/server/internal/config"
	"feathernote/server/internal/httpx"
	"feathernote/server/internal/plans"
)

var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidSHA reports whether s is a blob name: 64 lowercase hex characters.
func ValidSHA(s string) bool { return shaPattern.MatchString(s) }

// Service uploads and serves one account's blobs.
type Service struct {
	DB      *pgxpool.Pool
	Storage Storage
	Config  config.Config
	Clock   func() time.Time
}

// Put stores a blob after checking its size, its hash, the plan and the quota. A blob the account already has
// is accepted without storing anything.
func (s *Service) Put(ctx context.Context, accountID, sha string, body io.Reader) (int64, error) {
	// A blob the account already has: refresh its upload time under the account lock, so a client retrying an
	// upload before its push keeps the blob from the janitor (which takes the same lock).
	if size, ok, err := s.touch(ctx, accountID, sha); err != nil || ok {
		return size, err
	}
	var size int64
	// spool to disk while hashing: blobs can be large, and nothing is stored until the hash matches
	file, err := os.CreateTemp("", "feather-blob-*")
	if err != nil {
		return 0, err
	}
	defer func() { file.Close(); os.Remove(file.Name()) }()
	hasher := sha256.New()
	size, err = io.Copy(io.MultiWriter(file, hasher), io.LimitReader(body, s.Config.MaxBlobBytes+1))
	if err != nil {
		return 0, httpx.InvalidRequest("Unreadable body.")
	}
	if size > s.Config.MaxBlobBytes {
		return 0, httpx.ErrBlobTooLarge
	}
	if hex.EncodeToString(hasher.Sum(nil)) != sha {
		return 0, httpx.ErrBlobHashMismatch
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// one upload or push at a time per account, so parallel requests can't pass the quota together
	var deleted *time.Time
	if err := tx.QueryRow(ctx, `SELECT deleted_at FROM accounts WHERE id = $1 FOR UPDATE`, accountID).Scan(&deleted); err != nil {
		return 0, err
	}
	if deleted != nil {
		return 0, httpx.ErrUnauthorized
	}
	now := s.Clock()
	state, _, _, quota, err := plans.State(ctx, tx, accountID, s.Config, now)
	if err != nil {
		return 0, err
	}
	if state == "read_only" {
		return 0, httpx.ErrReadOnly
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM blobs WHERE account_id = $1 AND sha256 = $2)`, accountID, sha).Scan(&exists); err != nil || exists {
		return size, err
	}
	used, err := plans.UsedBytes(ctx, tx, accountID, now)
	if err != nil {
		return 0, err
	}
	if used+size > quota {
		return 0, httpx.ErrQuotaExceeded
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	if err := s.Storage.Put(ctx, Key(accountID, sha), file, size); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO blobs (account_id, sha256, size, uploaded_at) VALUES ($1, $2, $3, $4)`, accountID, sha, size, now); err != nil {
		return 0, err
	}
	return size, tx.Commit(ctx)
}

// Open returns a blob this account uploaded; any other is not_found.
func (s *Service) Open(ctx context.Context, accountID, sha string) (io.ReadCloser, int64, error) {
	var size int64
	err := s.DB.QueryRow(ctx, `SELECT size FROM blobs WHERE account_id = $1 AND sha256 = $2`, accountID, sha).Scan(&size)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, httpx.ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	r, _, err := s.Storage.Get(ctx, Key(accountID, sha))
	return r, size, err
}

// touch refreshes uploaded_at of an existing blob; ok is false when the account has no such blob.
func (s *Service) touch(ctx context.Context, accountID, sha string) (int64, bool, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var deleted *time.Time
	if err := tx.QueryRow(ctx, `SELECT deleted_at FROM accounts WHERE id = $1 FOR UPDATE`, accountID).Scan(&deleted); err != nil {
		return 0, false, err
	}
	if deleted != nil {
		return 0, false, httpx.ErrUnauthorized
	}
	var size int64
	err = tx.QueryRow(ctx, `UPDATE blobs SET uploaded_at = $3 WHERE account_id = $1 AND sha256 = $2 RETURNING size`, accountID, sha, s.Clock()).Scan(&size)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return size, true, tx.Commit(ctx)
}
