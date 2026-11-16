package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"feathernote/server/internal/httpx"
	"feathernote/server/internal/plans"
)

// dummyHash is compared against when an email has no account, so both failures do the same work.
var dummyHash = Hash([]byte("feather-no-account"))

// Prelogin returns the account's salt and parameters, or a stable fake salt and the defaults.
func (s *Service) Prelogin(ctx context.Context, email string) (KDF, []byte, error) {
	var salt, kdf []byte
	err := s.DB.QueryRow(ctx, `SELECT salt, kdf FROM accounts WHERE email_norm = $1 AND deleted_at IS NULL`, email).Scan(&salt, &kdf)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultKDF, FakeSalt(s.Config.ServerSecret, email), nil
	}
	if err != nil {
		return KDF{}, nil, err
	}
	var k KDF
	if err := jsonUnmarshal(kdf, &k); err != nil {
		return KDF{}, nil, err
	}
	return k, salt, nil
}

// Login signs in with auth_key. Wrong email and wrong key are the same 401; a right key for a signup never
// verified is 403 email_unverified, whether or not the email has an account (see Signup).
func (s *Service) Login(ctx context.Context, email string, authKey []byte, deviceName string) (Session, plans.Account, []byte, error) {
	if !validDeviceName(deviceName) {
		return Session{}, plans.Account{}, nil, httpx.InvalidRequest("Invalid device name.")
	}
	var accountID string
	var stored, wrapped []byte
	err := s.DB.QueryRow(ctx, `SELECT id, auth_hash, wrapped_master_password FROM accounts WHERE email_norm = $1 AND deleted_at IS NULL`, email).
		Scan(&accountID, &stored, &wrapped)
	if errors.Is(err, pgx.ErrNoRows) {
		Equal(Hash(authKey), dummyHash)
		return Session{}, plans.Account{}, nil, s.loginFailed(ctx, email, authKey)
	}
	if err != nil {
		return Session{}, plans.Account{}, nil, err
	}
	if !Equal(Hash(authKey), stored) {
		return Session{}, plans.Account{}, nil, s.loginFailed(ctx, email, authKey)
	}
	session, account, err := s.startSession(ctx, accountID, deviceName, "full", "auth_hash", authKey)
	return session, account, wrapped, err
}

// loginFailed answers a key that opens no account: 403 email_unverified for the email's pending signup's key, else
// 401. Both failures (no account, wrong key) come here, so they do the same database work.
func (s *Service) loginFailed(ctx context.Context, email string, authKey []byte) error {
	var pending []byte
	err := s.DB.QueryRow(ctx, `SELECT auth_hash FROM pending_signups WHERE email_norm = $1`, email).Scan(&pending)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil && Equal(Hash(authKey), pending) {
		return httpx.ErrEmailUnverified
	}
	return httpx.ErrUnauthorized
}

// Recover signs in with recovery_auth_key; the session may only set a new password or sign out.
func (s *Service) Recover(ctx context.Context, email string, recoveryAuthKey []byte, deviceName string) (Session, []byte, error) {
	if !validDeviceName(deviceName) {
		return Session{}, nil, httpx.InvalidRequest("Invalid device name.")
	}
	var accountID string
	var stored, wrapped []byte
	err := s.DB.QueryRow(ctx, `SELECT id, recovery_auth_hash, wrapped_master_recovery FROM accounts WHERE email_norm = $1 AND deleted_at IS NULL`, email).
		Scan(&accountID, &stored, &wrapped)
	if errors.Is(err, pgx.ErrNoRows) {
		Equal(Hash(recoveryAuthKey), dummyHash)
		return Session{}, nil, httpx.ErrUnauthorized
	}
	if err != nil {
		return Session{}, nil, err
	}
	if !Equal(Hash(recoveryAuthKey), stored) {
		return Session{}, nil, httpx.ErrUnauthorized
	}
	session, _, err := s.startSession(ctx, accountID, deviceName, "recovery", "recovery_auth_hash", recoveryAuthKey)
	return session, wrapped, err
}

// startSession creates the device and session. The caller's key check ran outside this transaction, so it checks the
// key again here with the account row locked FOR SHARE: a password change, recovery or deletion that committed in
// between wins, and one that starts after waits for this session and then revokes it with the rest. column is
// "auth_hash" or "recovery_auth_hash".
func (s *Service) startSession(ctx context.Context, accountID, deviceName, scope, column string, key []byte) (Session, plans.Account, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Session{}, plans.Account{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var stored []byte
	err = tx.QueryRow(ctx, `SELECT `+column+` FROM accounts WHERE id = $1 AND deleted_at IS NULL FOR SHARE`, accountID).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !Equal(Hash(key), stored) {
		return Session{}, plans.Account{}, httpx.ErrUnauthorized
	}
	if err != nil {
		return Session{}, plans.Account{}, err
	}
	now := s.Clock()
	session, err := newSession(ctx, tx, accountID, deviceName, scope, now)
	if err != nil {
		return Session{}, plans.Account{}, err
	}
	account, err := plans.Current(ctx, tx, accountID, s.Config, now)
	if err != nil {
		return Session{}, plans.Account{}, err
	}
	return session, account, tx.Commit(ctx)
}
