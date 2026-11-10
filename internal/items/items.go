// Package items implements push and pull (sync-protocol.md "Sync"). The server stores only the latest state
// of each item and cannot read meta or blobs.
package items

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"feathernote/server/internal/auth"
	"feathernote/server/internal/blobs"
	"feathernote/server/internal/config"
	"feathernote/server/internal/httpx"
	"feathernote/server/internal/plans"
)

const (
	MaxChanges   = 100
	MaxMetaBytes = 65536
	DefaultLimit = 500
	MaxLimit     = 1000
)

// Change is one entry of a push.
type Change struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	BaseVersion int64      `json:"base_version"`
	Deleted     bool       `json:"deleted"`
	Meta        *httpx.B64 `json:"meta"`
	BlobSHA256  *string    `json:"blob_sha256"`
}

// BlobRef is the protocol's BlobRef.
type BlobRef struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Item is the protocol's Item.
type Item struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	Version   int64      `json:"version"`
	Seq       int64      `json:"seq"`
	Deleted   bool       `json:"deleted"`
	Meta      *httpx.B64 `json:"meta"`
	Blob      *BlobRef   `json:"blob"`
	UpdatedAt string     `json:"updated_at"`
}

// Result is one push result: accepted (Version, Seq), conflict (Current) or rejected (Error).
type Result struct {
	ID      string            `json:"id"`
	Status  string            `json:"status"`
	Version int64             `json:"version,omitempty"`
	Seq     int64             `json:"seq,omitempty"`
	Current *Item             `json:"current,omitempty"`
	Error   map[string]string `json:"error,omitempty"`
}

func rejected(id, code string) Result {
	return Result{ID: id, Status: "rejected", Error: map[string]string{"code": code}}
}

// Page is a pull response.
type Page struct {
	Items   []Item `json:"items"`
	Cursor  string `json:"cursor"`
	HasMore bool   `json:"has_more"`
}

// Service pushes and pulls one account's items.
type Service struct {
	DB     *pgxpool.Pool
	Config config.Config
	Clock  func() time.Time
}

var types = map[string]bool{"note": true, "notebook": true, "tag": true, "attachment": true}

// check applies the field rules a change must meet on its own (rules 4 and 5, shape).
func check(c Change) bool {
	if !auth.ValidID(c.ID) || !types[c.Type] || c.BaseVersion < 0 {
		return false
	}
	if c.Deleted {
		return c.Meta == nil && c.BlobSHA256 == nil
	}
	if c.Meta == nil || len(*c.Meta) > MaxMetaBytes {
		return false
	}
	needsBlob := c.Type == "note" || c.Type == "attachment"
	if needsBlob != (c.BlobSHA256 != nil) {
		return false
	}
	return c.BlobSHA256 == nil || blobs.ValidSHA(*c.BlobSHA256)
}

// lockAccount serialises pushes, uploads and quota checks on one account.
func lockAccount(ctx context.Context, tx pgx.Tx, accountID, libraryID string) error {
	var library string
	var deleted *time.Time
	if err := tx.QueryRow(ctx, `SELECT library_id, deleted_at FROM accounts WHERE id = $1 FOR UPDATE`, accountID).Scan(&library, &deleted); err != nil {
		return err
	}
	if deleted != nil {
		return httpx.ErrUnauthorized
	}
	if library != libraryID {
		return httpx.ErrLibraryMismatch
	}
	return nil
}

// Push applies each change on its own, in order; a rejected change leaves the others applied.
func (s *Service) Push(ctx context.Context, accountID, libraryID string, changes []Change) ([]Result, error) {
	if len(changes) > MaxChanges {
		return nil, httpx.InvalidRequest(fmt.Sprintf("At most %d changes per push.", MaxChanges))
	}
	// every result carries its change's id, so a change without a valid id can't have one: refuse the request
	for _, c := range changes {
		if !auth.ValidID(c.ID) {
			return nil, httpx.InvalidRequest("Every change needs a ULID id.")
		}
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAccount(ctx, tx, accountID, libraryID); err != nil {
		return nil, err
	}
	now := s.Clock()
	state, _, _, quota, err := plans.State(ctx, tx, accountID, s.Config, now)
	if err != nil {
		return nil, err
	}
	if state == "read_only" {
		return nil, httpx.ErrReadOnly
	}
	results := make([]Result, 0, len(changes))
	for _, c := range changes {
		r, err := s.apply(ctx, tx, accountID, c, quota, now)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, tx.Commit(ctx)
}

func (s *Service) apply(ctx context.Context, tx pgx.Tx, accountID string, c Change, quota int64, now time.Time) (Result, error) {
	if !check(c) {
		return rejected(c.ID, "invalid_request"), nil
	}
	current, err := get(ctx, tx, accountID, c.ID)
	if err != nil {
		return Result{}, err
	}
	switch {
	case c.BaseVersion == 0 && current != nil:
		return Result{ID: c.ID, Status: "conflict", Current: current}, nil
	case c.BaseVersion > 0 && current == nil:
		return rejected(c.ID, "not_found"), nil
	case current != nil && current.Type != c.Type:
		return rejected(c.ID, "type_mismatch"), nil
	case current != nil && current.Version != c.BaseVersion:
		return Result{ID: c.ID, Status: "conflict", Current: current}, nil
	}
	if c.BlobSHA256 != nil {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM blobs WHERE account_id = $1 AND sha256 = $2)`, accountID, *c.BlobSHA256).Scan(&ok); err != nil {
			return Result{}, err
		}
		if !ok {
			return rejected(c.ID, "blob_missing"), nil
		}
	}
	before, err := plans.UsedBytes(ctx, tx, accountID, now)
	if err != nil {
		return Result{}, err
	}
	if _, err := tx.Exec(ctx, "SAVEPOINT change"); err != nil {
		return Result{}, err
	}
	var seq int64
	if err := tx.QueryRow(ctx, `UPDATE accounts SET last_seq = last_seq + 1 WHERE id = $1 RETURNING last_seq`, accountID).Scan(&seq); err != nil {
		return Result{}, err
	}
	var meta []byte
	if c.Meta != nil {
		meta = *c.Meta
	}
	version := c.BaseVersion + 1
	_, err = tx.Exec(ctx, `INSERT INTO items (account_id, id, type, version, seq, deleted, meta, blob_sha256, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (account_id, id) DO UPDATE SET version = $4, seq = $5, deleted = $6, meta = $7, blob_sha256 = $8, updated_at = $9`,
		accountID, c.ID, c.Type, version, seq, c.Deleted, meta, c.BlobSHA256, now)
	if err != nil {
		return Result{}, err
	}
	after, err := plans.UsedBytes(ctx, tx, accountID, now)
	if err != nil {
		return Result{}, err
	}
	// a change that frees space (a delete) is always allowed, even over quota
	if after > quota && after > before {
		if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT change"); err != nil {
			return Result{}, err
		}
		return rejected(c.ID, "quota_exceeded"), nil
	}
	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT change"); err != nil {
		return Result{}, err
	}
	return Result{ID: c.ID, Status: "accepted", Version: version, Seq: seq}, nil
}

const itemColumns = `i.id, i.type, i.version, i.seq, i.deleted, i.meta, i.blob_sha256, b.size, i.updated_at`

func scanItem(row pgx.Row) (*Item, error) {
	var it Item
	var meta []byte
	var sha *string
	var size *int64
	var updated time.Time
	if err := row.Scan(&it.ID, &it.Type, &it.Version, &it.Seq, &it.Deleted, &meta, &sha, &size, &updated); err != nil {
		return nil, err
	}
	if meta != nil {
		m := httpx.B64(meta)
		it.Meta = &m
	}
	if sha != nil && size != nil {
		it.Blob = &BlobRef{*sha, *size}
	}
	it.UpdatedAt = httpx.Time(updated)
	return &it, nil
}

func get(ctx context.Context, tx pgx.Tx, accountID, id string) (*Item, error) {
	it, err := scanItem(tx.QueryRow(ctx, `SELECT `+itemColumns+` FROM items i
		LEFT JOIN blobs b ON b.account_id = i.account_id AND b.sha256 = i.blob_sha256
		WHERE i.account_id = $1 AND i.id = $2`, accountID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return it, err
}

var cursorPattern = regexp.MustCompile(`^c_[0-9]{12}$`)

// Cursor is the opaque text form of a seq.
func Cursor(seq int64) string { return fmt.Sprintf("c_%012d", seq) }

// ParsePull checks the cursor and limit query values.
func ParsePull(cursor, limit string) (int64, int, error) {
	var seq int64
	if cursor != "" {
		if !cursorPattern.MatchString(cursor) {
			return 0, 0, httpx.InvalidRequest("Invalid cursor.")
		}
		seq, _ = strconv.ParseInt(cursor[2:], 10, 64)
	}
	n := DefaultLimit
	if limit != "" {
		v, err := strconv.Atoi(limit)
		if err != nil || v < 1 || v > MaxLimit {
			return 0, 0, httpx.InvalidRequest("limit must be 1 to 1000.")
		}
		n = v
	}
	return seq, n, nil
}

// Pull returns items changed after the cursor, in seq order; each item appears once, in its latest state.
func (s *Service) Pull(ctx context.Context, accountID, libraryID string, after int64, limit int) (Page, error) {
	var library string
	if err := s.DB.QueryRow(ctx, `SELECT library_id FROM accounts WHERE id = $1`, accountID).Scan(&library); err != nil {
		return Page{}, err
	}
	if library != libraryID {
		return Page{}, httpx.ErrLibraryMismatch
	}
	rows, err := s.DB.Query(ctx, `SELECT `+itemColumns+` FROM items i
		LEFT JOIN blobs b ON b.account_id = i.account_id AND b.sha256 = i.blob_sha256
		WHERE i.account_id = $1 AND i.seq > $2 ORDER BY i.seq LIMIT $3`, accountID, after, limit+1)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	page := Page{Items: []Item{}, Cursor: Cursor(after)}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return Page{}, err
		}
		if len(page.Items) == limit {
			page.HasMore = true
			break
		}
		page.Items = append(page.Items, *it)
		page.Cursor = Cursor(it.Seq)
	}
	return page, rows.Err()
}
