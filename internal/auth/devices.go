package auth

import (
	"context"
	"time"

	"feathernote/server/internal/httpx"
)

// Device is the protocol's Device object.
type Device struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  string `json:"created_at"`
	LastSeenAt string `json:"last_seen_at"`
	Current    bool   `json:"current"`
}

// Devices lists the account's signed-in devices, oldest first.
func (s *Service) Devices(ctx context.Context, sess Session) ([]Device, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, name, created_at, last_seen_at FROM devices WHERE account_id = $1 ORDER BY created_at, id`, sess.AccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Device{}
	for rows.Next() {
		var d Device
		var created, seen time.Time
		if err := rows.Scan(&d.ID, &d.Name, &created, &seen); err != nil {
			return nil, err
		}
		d.CreatedAt, d.LastSeenAt, d.Current = httpx.Time(created), httpx.Time(seen), d.ID == sess.DeviceID
		list = append(list, d)
	}
	return list, rows.Err()
}

// RevokeDevice signs one of the account's devices out; another account's device is 404.
func (s *Service) RevokeDevice(ctx context.Context, sess Session, deviceID string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM devices WHERE id = $1 AND account_id = $2`, deviceID, sess.AccountID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrNotFound
	}
	return nil
}
