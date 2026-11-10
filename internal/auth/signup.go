package auth

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"

	"feathernote/server/internal/config"
	"feathernote/server/internal/httpx"
	"feathernote/server/internal/mail"
	"feathernote/server/internal/plans"
)

const (
	codeLifetime    = 15 * time.Minute
	pendingLifetime = 24 * time.Hour
	maxAttempts     = 5
)

// Service implements the account lifecycle. Every method's errors are *httpx.Error or internal errors.
type Service struct {
	DB     *pgxpool.Pool
	Mailer mail.Mailer
	Clock  func() time.Time
	Config config.Config
}

// KDF is the protocol's Kdf object.
type KDF struct {
	Alg  string `json:"alg"`
	T    int    `json:"t"`
	MKiB int    `json:"m_kib"`
	P    int    `json:"p"`
}

// DefaultKDF is what prelogin returns for unknown emails (crypto.md "Password derivation").
var DefaultKDF = KDF{"argon2id13", 3, 262144, 1}

func (k KDF) valid() bool { return k.Alg == "argon2id13" && k.T >= 1 && k.MKiB >= 8 && k.P == 1 }

// SignupRequest is the body of POST /auth/signup.
type SignupRequest struct {
	Email                 string    `json:"email"`
	Salt                  httpx.B64 `json:"salt"`
	KDF                   KDF       `json:"kdf"`
	AuthKey               httpx.B64 `json:"auth_key"`
	RecoveryAuthKey       httpx.B64 `json:"recovery_auth_key"`
	WrappedMasterPassword httpx.B64 `json:"wrapped_master_password"`
	WrappedMasterRecovery httpx.B64 `json:"wrapped_master_recovery"`
	LibraryID             string    `json:"library_id"`
}

// Session is one device's sign-in.
type Session struct {
	Token     string
	DeviceID  string
	AccountID string
	Scope     string // "full" or "recovery"
}

// ValidID reports whether s is ULID text (crypto.md "Ids").
func ValidID(s string) bool {
	_, err := ulid.ParseStrict(s)
	return err == nil && len(s) == 26
}

func validDeviceName(s string) bool { return utf8.RuneCountInString(s) <= 100 && utf8.ValidString(s) }

// Signup starts an account. It succeeds the same way whether or not the email has one (it then sends a
// notice instead of a code), so signup can't be used to find accounts.
func (s *Service) Signup(ctx context.Context, r SignupRequest) error {
	email, err := NormalizeEmail(r.Email)
	if err != nil || len(r.Salt) != 16 || !r.KDF.valid() || len(r.AuthKey) != 32 || len(r.RecoveryAuthKey) != 32 ||
		len(r.WrappedMasterPassword) != 74 || len(r.WrappedMasterRecovery) != 74 || !ValidID(r.LibraryID) {
		return httpx.InvalidRequest("Invalid signup fields.")
	}
	var exists bool
	if err := s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE email_norm = $1 AND deleted_at IS NULL)`, email).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return s.Mailer.SendAccountExists(ctx, email)
	}
	code, codeHash := NewCode()
	kdf, _ := json.Marshal(r.KDF)
	now := s.Clock()
	_, err = s.DB.Exec(ctx, `
		INSERT INTO pending_signups (email_norm, salt, kdf, auth_hash, recovery_auth_hash, wrapped_master_password,
		       wrapped_master_recovery, library_id, code_hash, code_sent_at, attempts, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 0, $10)
		ON CONFLICT (email_norm) DO UPDATE SET salt = $2, kdf = $3, auth_hash = $4, recovery_auth_hash = $5,
		       wrapped_master_password = $6, wrapped_master_recovery = $7, library_id = $8, code_hash = $9,
		       code_sent_at = $10, attempts = 0, created_at = $10`,
		email, []byte(r.Salt), kdf, Hash(r.AuthKey), Hash(r.RecoveryAuthKey), []byte(r.WrappedMasterPassword),
		[]byte(r.WrappedMasterRecovery), r.LibraryID, codeHash, now)
	if err != nil {
		return err
	}
	return s.Mailer.SendCode(ctx, email, code)
}

// Resend sends a new code for a pending signup; for any other email it does nothing, the same way.
func (s *Service) Resend(ctx context.Context, emailText string) error {
	email, err := NormalizeEmail(emailText)
	if err != nil {
		return httpx.InvalidRequest("Invalid email.")
	}
	code, codeHash := NewCode()
	now := s.Clock()
	tag, err := s.DB.Exec(ctx, `UPDATE pending_signups SET code_hash = $2, code_sent_at = $3, attempts = 0
		WHERE email_norm = $1 AND created_at > $4`, email, codeHash, now, now.Add(-pendingLifetime))
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	return s.Mailer.SendCode(ctx, email, code)
}

// Verify confirms the code and creates the account, its first device and a full-scope session.
func (s *Service) Verify(ctx context.Context, emailText, code, deviceName string) (Session, plans.Account, error) {
	email, err := NormalizeEmail(emailText)
	if err != nil || len(code) != 6 || !validDeviceName(deviceName) {
		return Session{}, plans.Account{}, httpx.InvalidRequest("Invalid fields.")
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return Session{}, plans.Account{}, httpx.InvalidRequest("Invalid code.")
		}
	}
	now := s.Clock()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Session{}, plans.Account{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var p struct {
		salt, kdf, authHash, recoveryHash, wrappedPw, wrappedRecov, codeHash []byte
		library                                                              string
		sentAt, createdAt                                                    time.Time
		attempts                                                             int
	}
	err = tx.QueryRow(ctx, `SELECT salt, kdf, auth_hash, recovery_auth_hash, wrapped_master_password, wrapped_master_recovery,
		library_id, code_hash, code_sent_at, attempts, created_at FROM pending_signups WHERE email_norm = $1 FOR UPDATE`, email).
		Scan(&p.salt, &p.kdf, &p.authHash, &p.recoveryHash, &p.wrappedPw, &p.wrappedRecov, &p.library, &p.codeHash, &p.sentAt, &p.attempts, &p.createdAt)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && now.Sub(p.createdAt) >= pendingLifetime {
		return Session{}, plans.Account{}, httpx.ErrVerificationExpired
	}
	if err != nil {
		return Session{}, plans.Account{}, err
	}
	if p.attempts >= maxAttempts {
		return Session{}, plans.Account{}, httpx.ErrVerificationInvalid
	}
	if now.Sub(p.sentAt) > codeLifetime {
		return Session{}, plans.Account{}, httpx.ErrVerificationExpired
	}
	if !Equal(Hash([]byte(code)), p.codeHash) {
		if _, err := tx.Exec(ctx, `UPDATE pending_signups SET attempts = attempts + 1 WHERE email_norm = $1`, email); err != nil {
			return Session{}, plans.Account{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Session{}, plans.Account{}, err
		}
		return Session{}, plans.Account{}, httpx.ErrVerificationInvalid
	}
	state, reason, trialEnds, err := plans.Initial(ctx, tx, EmailHash(email), s.Config, now)
	if err != nil {
		return Session{}, plans.Account{}, err
	}
	accountID := NewID()
	_, err = tx.Exec(ctx, `INSERT INTO accounts (id, email_norm, salt, kdf, auth_hash, recovery_auth_hash, wrapped_master_password,
		wrapped_master_recovery, library_id, plan_state, plan_reason, trial_ends_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		accountID, email, p.salt, p.kdf, p.authHash, p.recoveryHash, p.wrappedPw, p.wrappedRecov, p.library, state, reason, trialEnds, now)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // verified meanwhile through another request
		return Session{}, plans.Account{}, httpx.ErrVerificationExpired
	}
	if err != nil {
		return Session{}, plans.Account{}, err
	}
	session, err := newSession(ctx, tx, accountID, deviceName, "full", now)
	if err != nil {
		return Session{}, plans.Account{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM pending_signups WHERE email_norm = $1`, email); err != nil {
		return Session{}, plans.Account{}, err
	}
	account, err := plans.Current(ctx, tx, accountID, s.Config, now)
	if err != nil {
		return Session{}, plans.Account{}, err
	}
	return session, account, tx.Commit(ctx)
}

// newSession creates a device and its session.
func newSession(ctx context.Context, tx pgx.Tx, accountID, deviceName, scope string, now time.Time) (Session, error) {
	token, hash := NewToken()
	deviceID := NewID()
	if _, err := tx.Exec(ctx, `INSERT INTO devices (id, account_id, name, created_at, last_seen_at) VALUES ($1, $2, $3, $4, $4)`,
		deviceID, accountID, deviceName, now); err != nil {
		return Session{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO sessions (token_hash, device_id, account_id, scope, created_at, last_used_at) VALUES ($1, $2, $3, $4, $5, $5)`,
		hash, deviceID, accountID, scope, now); err != nil {
		return Session{}, err
	}
	return Session{Token: token, DeviceID: deviceID, AccountID: accountID, Scope: scope}, nil
}
