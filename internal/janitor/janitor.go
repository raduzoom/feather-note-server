// Package janitor deletes what the protocol says goes away later: deleted accounts' data, blobs nothing
// references after 24 hours, pending signups after 24 hours, and sessions unused for 90 days.
package janitor

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"feathernote/server/internal/blobs"
)

// Janitor runs the deletions.
type Janitor struct {
	DB      *pgxpool.Pool
	Storage blobs.Storage
	Clock   func() time.Time
	Log     *slog.Logger
}

// Run does one pass. A deleted account's objects go before its rows, so a crash in between leaves rows the
// next pass finishes. An orphan blob's row goes first, under the account lock (see forgetBlob).
func (j *Janitor) Run(ctx context.Context) error {
	now := j.Clock()
	if err := j.deletedAccounts(ctx); err != nil {
		return err
	}
	if err := j.orphanBlobs(ctx, now.Add(-24*time.Hour)); err != nil {
		return err
	}
	if _, err := j.DB.Exec(ctx, `DELETE FROM pending_signups WHERE created_at < $1`, now.Add(-24*time.Hour)); err != nil {
		return err
	}
	_, err := j.DB.Exec(ctx, `DELETE FROM devices WHERE id IN (SELECT device_id FROM sessions WHERE last_used_at < $1)`, now.Add(-90*24*time.Hour))
	return err
}

func (j *Janitor) deletedAccounts(ctx context.Context) error {
	rows, err := j.DB.Query(ctx, `SELECT id FROM accounts WHERE deleted_at IS NOT NULL`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		for {
			keys, err := j.Storage.List(ctx, id+"/")
			if err != nil {
				return err
			}
			if len(keys) == 0 {
				break
			}
			for _, k := range keys {
				if err := j.Storage.Delete(ctx, k); err != nil {
					return err
				}
			}
		}
		if _, err := j.DB.Exec(ctx, `DELETE FROM accounts WHERE id = $1 AND deleted_at IS NOT NULL`, id); err != nil {
			return err
		}
		j.Log.Info("account deleted", "account_id", id)
	}
	return nil
}

func (j *Janitor) orphanBlobs(ctx context.Context, before time.Time) error {
	rows, err := j.DB.Query(ctx, `SELECT b.account_id, b.sha256 FROM blobs b WHERE b.uploaded_at < $1 AND NOT EXISTS (
		SELECT 1 FROM items i WHERE i.account_id = b.account_id AND NOT i.deleted AND i.blob_sha256 = b.sha256)`, before)
	if err != nil {
		return err
	}
	type blob struct{ account, sha string }
	var list []blob
	for rows.Next() {
		var b blob
		if err := rows.Scan(&b.account, &b.sha); err != nil {
			rows.Close()
			return err
		}
		list = append(list, b)
	}
	rows.Close()
	for _, b := range list {
		gone, err := j.forgetBlob(ctx, b.account, b.sha, before)
		if err != nil {
			return err
		}
		if gone {
			if err := j.Storage.Delete(ctx, blobs.Key(b.account, b.sha)); err != nil {
				return err
			}
		}
	}
	return nil
}

// forgetBlob deletes an orphan's row under the account lock that pushes and uploads take, re-checking that it is
// still old and unreferenced. Only after that commits is the object deleted, so no push can accept a reference
// to a blob whose object is gone. A crash before the object delete leaves an object nothing counts or serves.
func (j *Janitor) forgetBlob(ctx context.Context, accountID, sha string, before time.Time) (bool, error) {
	tx, err := j.DB.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM accounts WHERE id = $1 FOR UPDATE`, accountID); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM blobs b WHERE b.account_id = $1 AND b.sha256 = $2 AND b.uploaded_at < $3 AND NOT EXISTS (
		SELECT 1 FROM items i WHERE i.account_id = b.account_id AND NOT i.deleted AND i.blob_sha256 = b.sha256)`, accountID, sha, before)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, tx.Commit(ctx)
}
