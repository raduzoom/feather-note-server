package auth

import (
	"context"
	"encoding/json"

	"feathernote/server/internal/httpx"
)

// SetPasswordRequest is the body of POST /account/password.
type SetPasswordRequest struct {
	CurrentAuthKey        httpx.B64 `json:"current_auth_key"`
	Salt                  httpx.B64 `json:"salt"`
	KDF                   KDF       `json:"kdf"`
	AuthKey               httpx.B64 `json:"auth_key"`
	WrappedMasterPassword httpx.B64 `json:"wrapped_master_password"`
}

// SetPassword stores new password data. A full-scope session proves the old password with current_auth_key;
// a recovery-scope one needs none. Every other session is revoked, and this device gets a new full-scope token.
func (s *Service) SetPassword(ctx context.Context, sess Session, r SetPasswordRequest) (string, error) {
	if len(r.Salt) != 16 || !r.KDF.valid() || len(r.AuthKey) != 32 || len(r.WrappedMasterPassword) != 74 {
		return "", httpx.InvalidRequest("Invalid password fields.")
	}
	if sess.Scope == "full" && r.CurrentAuthKey == nil {
		return "", httpx.InvalidRequest("current_auth_key is required.")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var stored []byte
	if err := tx.QueryRow(ctx, `SELECT auth_hash FROM accounts WHERE id = $1 FOR UPDATE`, sess.AccountID).Scan(&stored); err != nil {
		return "", err
	}
	if sess.Scope == "full" && !Equal(Hash(r.CurrentAuthKey), stored) {
		return "", httpx.ErrUnauthorized
	}
	kdf, _ := json.Marshal(r.KDF)
	if _, err := tx.Exec(ctx, `UPDATE accounts SET salt = $2, kdf = $3, auth_hash = $4, wrapped_master_password = $5 WHERE id = $1`,
		sess.AccountID, []byte(r.Salt), kdf, Hash(r.AuthKey), []byte(r.WrappedMasterPassword)); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM devices WHERE account_id = $1 AND id <> $2`, sess.AccountID, sess.DeviceID); err != nil {
		return "", err
	}
	token, hash := NewToken()
	now := s.Clock()
	if _, err := tx.Exec(ctx, `UPDATE sessions SET token_hash = $2, scope = 'full', created_at = $3, last_used_at = $3 WHERE device_id = $1`,
		sess.DeviceID, hash, now); err != nil {
		return "", err
	}
	return token, tx.Commit(ctx)
}

// DeleteAccount checks auth_key, marks the account deleted and signs every device out at once. The janitor
// removes its items, blobs and rows; the trial record stays.
func (s *Service) DeleteAccount(ctx context.Context, sess Session, authKey []byte) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var stored []byte
	if err := tx.QueryRow(ctx, `SELECT auth_hash FROM accounts WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, sess.AccountID).Scan(&stored); err != nil {
		return httpx.ErrUnauthorized
	}
	if !Equal(Hash(authKey), stored) {
		return httpx.ErrUnauthorized
	}
	if _, err := tx.Exec(ctx, `UPDATE accounts SET deleted_at = $2 WHERE id = $1`, sess.AccountID, s.Clock()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM devices WHERE account_id = $1`, sess.AccountID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
