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
// verified is 403 email_unverified.
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
		var pending []byte
		if s.DB.QueryRow(ctx, `SELECT auth_hash FROM pending_signups WHERE email_norm = $1`, email).Scan(&pending) == nil && Equal(Hash(authKey), pending) {
			return Session{}, plans.Account{}, nil, httpx.ErrEmailUnverified
		}
		return Session{}, plans.Account{}, nil, httpx.ErrUnauthorized
	}
	if err != nil {
		return Session{}, plans.Account{}, nil, err
	}
	if !Equal(Hash(authKey), stored) {
		return Session{}, plans.Account{}, nil, httpx.ErrUnauthorized
	}
	session, account, err := s.startSession(ctx, accountID, deviceName, "full")
	return session, account, wrapped, err
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
	session, _, err := s.startSession(ctx, accountID, deviceName, "recovery")
	return session, wrapped, err
}

func (s *Service) startSession(ctx context.Context, accountID, deviceName, scope string) (Session, plans.Account, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Session{}, plans.Account{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
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
