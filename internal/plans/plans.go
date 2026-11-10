// Package plans holds plan state, quotas and usage (sync-protocol.md "Plan states", "Quota").
package plans

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"feathernote/server/internal/config"
	"feathernote/server/internal/httpx"
)

// Querier is what *pgxpool.Pool and pgx.Tx share.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const (
	TrialQuota = 1073741824
	PaidQuota  = 10737418240
	TrialDays  = 14
)

// Plan is the protocol's Plan object.
type Plan struct {
	State       string  `json:"state"`
	Reason      *string `json:"reason"`
	TrialEndsAt *string `json:"trial_ends_at"`
	QuotaBytes  int64   `json:"quota_bytes"`
	UsedBytes   int64   `json:"used_bytes"`
}

// Account is the protocol's Account object.
type Account struct {
	Email          string `json:"email"`
	LibraryID      string `json:"library_id"`
	Plan           Plan   `json:"plan"`
	BillingEnabled bool   `json:"billing_enabled"`
}

func str(s string) *string { return &s }

// Initial is a new account's plan: active with billing off; read-only if this email had a trial; else a
// 14-day trial, recorded so the email never gets another.
func Initial(ctx context.Context, tx pgx.Tx, emailHash []byte, cfg config.Config, now time.Time) (string, *string, *time.Time, error) {
	if !cfg.BillingEnabled {
		return "active", nil, nil, nil
	}
	tag, err := tx.Exec(ctx, `INSERT INTO trial_records (email_hash, used_at) VALUES ($1, $2) ON CONFLICT DO NOTHING`, emailHash, now)
	if err != nil {
		return "", nil, nil, err
	}
	if tag.RowsAffected() == 0 {
		return "read_only", str("trial_already_used"), nil, nil
	}
	ends := now.Add(TrialDays * 24 * time.Hour)
	return "trial", nil, &ends, nil
}

// QuotaBytes for a state; read-only keeps the quota of the state it came from.
func QuotaBytes(state string, reason *string, cfg config.Config) int64 {
	switch {
	case !cfg.BillingEnabled:
		return cfg.QuotaBytes
	case state == "active", state == "read_only" && reason != nil && *reason == "subscription_ended":
		return PaidQuota
	default:
		return TrialQuota
	}
}

// UsedBytes: meta of live items, every blob a live item references, and every blob uploaded in the last
// 24 hours that nothing references yet. Tombstones count nothing.
func UsedBytes(ctx context.Context, q Querier, accountID string, now time.Time) (int64, error) {
	var used int64
	err := q.QueryRow(ctx, `
		SELECT COALESCE((SELECT sum(length(meta)) FROM items WHERE account_id = $1 AND NOT deleted), 0)
		     + COALESCE((SELECT sum(b.size) FROM blobs b WHERE b.account_id = $1 AND (b.uploaded_at > $2 OR EXISTS (
		           SELECT 1 FROM items i WHERE i.account_id = $1 AND NOT i.deleted AND i.blob_sha256 = b.sha256))), 0)`,
		accountID, now.Add(-24*time.Hour)).Scan(&used)
	return used, err
}

// SetState changes an account's plan (Phase 9's Stripe webhooks call it). Nothing is deleted by any state.
func SetState(ctx context.Context, q Querier, accountID, state string, reason *string) error {
	_, err := q.Exec(ctx, `UPDATE accounts SET plan_state = $2, plan_reason = $3 WHERE id = $1`, accountID, state, reason)
	return err
}

// State is the account's effective plan state and quota: billing off is always active; a trial past its end
// becomes read-only (trial_expired), stored so it stays that way.
func State(ctx context.Context, q Querier, accountID string, cfg config.Config, now time.Time) (state string, reason *string, trialEnds *time.Time, quota int64, err error) {
	err = q.QueryRow(ctx, `SELECT plan_state, plan_reason, trial_ends_at FROM accounts WHERE id = $1`, accountID).Scan(&state, &reason, &trialEnds)
	if err != nil {
		return
	}
	switch {
	case !cfg.BillingEnabled:
		state, reason = "active", nil
	case state == "trial" && trialEnds != nil && now.After(*trialEnds):
		state, reason = "read_only", str("trial_expired")
		if err = SetState(ctx, q, accountID, state, reason); err != nil {
			return
		}
	}
	return state, reason, trialEnds, QuotaBytes(state, reason, cfg), nil
}

// Current is the account as GET /account shows it.
func Current(ctx context.Context, q Querier, accountID string, cfg config.Config, now time.Time) (Account, error) {
	var a Account
	if err := q.QueryRow(ctx, `SELECT email_norm, library_id FROM accounts WHERE id = $1`, accountID).Scan(&a.Email, &a.LibraryID); err != nil {
		return a, err
	}
	state, reason, trialEnds, quota, err := State(ctx, q, accountID, cfg, now)
	if err != nil {
		return a, err
	}
	a.Plan.State, a.Plan.Reason, a.Plan.QuotaBytes = state, reason, quota
	if trialEnds != nil && cfg.BillingEnabled {
		a.Plan.TrialEndsAt = str(httpx.Time(*trialEnds))
	}
	if a.Plan.UsedBytes, err = UsedBytes(ctx, q, accountID, now); err != nil {
		return a, err
	}
	a.BillingEnabled = cfg.BillingEnabled
	return a, nil
}
