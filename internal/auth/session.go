package auth

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"feathernote/server/internal/httpx"
)

const sessionIdle = 90 * 24 * time.Hour

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// Authenticate resolves a bearer token. Unknown, malformed, revoked or 90-day-idle tokens, and tokens of a
// deleted account, are 401. Use is recorded at most once a minute.
func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
	hash, ok := HashToken(token)
	if !ok {
		return Session{}, httpx.ErrUnauthorized
	}
	var sess Session
	var lastUsed time.Time
	err := s.DB.QueryRow(ctx, `SELECT s.device_id, s.account_id, s.scope, s.last_used_at FROM sessions s
		JOIN accounts a ON a.id = s.account_id WHERE s.token_hash = $1 AND a.deleted_at IS NULL`, hash).
		Scan(&sess.DeviceID, &sess.AccountID, &sess.Scope, &lastUsed)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, httpx.ErrUnauthorized
	}
	if err != nil {
		return Session{}, err
	}
	now := s.Clock()
	if now.Sub(lastUsed) > sessionIdle {
		_, _ = s.DB.Exec(ctx, `DELETE FROM devices WHERE id = $1`, sess.DeviceID)
		return Session{}, httpx.ErrUnauthorized
	}
	if now.Sub(lastUsed) >= time.Minute {
		if _, err := s.DB.Exec(ctx, `UPDATE sessions SET last_used_at = $2 WHERE token_hash = $1`, hash, now); err != nil {
			return Session{}, err
		}
		if _, err := s.DB.Exec(ctx, `UPDATE devices SET last_seen_at = $2 WHERE id = $1`, sess.DeviceID, now); err != nil {
			return Session{}, err
		}
	}
	sess.Token = token
	return sess, nil
}

// Logout signs the calling device out (its device and session are removed).
func (s *Service) Logout(ctx context.Context, sess Session) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM devices WHERE id = $1 AND account_id = $2`, sess.DeviceID, sess.AccountID)
	return err
}
