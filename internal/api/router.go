// Package api holds the Feather Cloud routes and middleware. Behaviour lives in the domain packages.
package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"feathernote/server/internal/auth"
	"feathernote/server/internal/blobs"
	"feathernote/server/internal/config"
	"feathernote/server/internal/httpx"
	"feathernote/server/internal/items"
	"feathernote/server/internal/logx"
	"feathernote/server/internal/mail"
	"feathernote/server/internal/plans"
	"feathernote/server/internal/ratelimit"
)

// Deps is what the handlers need; it grows as packages are added.
type Deps struct {
	Config  config.Config
	DB      *pgxpool.Pool
	Mailer  mail.Mailer
	Storage blobs.Storage
	Clock   func() time.Time
	Log     *slog.Logger
	Version string
}

type handlers struct {
	d       Deps
	limiter *ratelimit.Limiter
	auth    *auth.Service
	blobs   *blobs.Service
	items   *items.Service
}

// fail writes an error; internal ones are logged by type only, never with their text (it may hold data).
func (h *handlers) fail(w http.ResponseWriter, err error) {
	var e *httpx.Error
	if !errors.As(err, &e) {
		h.d.Log.Error("internal error", "type", fmt.Sprintf("%T", err))
	}
	httpx.WriteError(w, err)
}

const (
	public          = iota // no token
	fullScope              // a full-scope session
	recoveryAllowed        // any session, including recovery scope
)

type route struct {
	pattern string // "METHOD /path", as in the OpenAPI file
	access  int
	handler func(*handlers) http.HandlerFunc
}

// routes is every protocol operation (sync-protocol.md "Endpoints"), served under /v1.
var routes = []route{
	{"GET /server", public, func(h *handlers) http.HandlerFunc { return h.serverInfo }},
	{"POST /auth/prelogin", public, func(h *handlers) http.HandlerFunc { return h.prelogin }},
	{"POST /auth/signup", public, func(h *handlers) http.HandlerFunc { return h.signup }},
	{"POST /auth/verify", public, func(h *handlers) http.HandlerFunc { return h.verify }},
	{"POST /auth/resend", public, func(h *handlers) http.HandlerFunc { return h.resend }},
	{"POST /auth/login", public, func(h *handlers) http.HandlerFunc { return h.login }},
	{"POST /auth/recover", public, func(h *handlers) http.HandlerFunc { return h.recover }},
	{"POST /auth/logout", recoveryAllowed, func(h *handlers) http.HandlerFunc { return h.logout }},
	{"GET /account", fullScope, func(h *handlers) http.HandlerFunc { return h.getAccount }},
	{"DELETE /account", fullScope, func(h *handlers) http.HandlerFunc { return h.deleteAccount }},
	{"POST /account/password", recoveryAllowed, func(h *handlers) http.HandlerFunc { return h.setPassword }},
	{"GET /devices", fullScope, func(h *handlers) http.HandlerFunc { return h.devices }},
	{"DELETE /devices/{device_id}", fullScope, func(h *handlers) http.HandlerFunc { return h.revokeDevice }},
	{"POST /sync/push", fullScope, func(h *handlers) http.HandlerFunc { return h.push }},
	{"GET /sync/pull", fullScope, func(h *handlers) http.HandlerFunc { return h.pull }},
	{"PUT /blobs/{sha256}", fullScope, func(h *handlers) http.HandlerFunc { return h.putBlob }},
	{"GET /blobs/{sha256}", fullScope, func(h *handlers) http.HandlerFunc { return h.getBlob }},
}

// Routes lists every operation the router serves under /v1, written as in the OpenAPI file.
func Routes() []string {
	out := make([]string, len(routes))
	for i, r := range routes {
		out[i] = r.pattern
	}
	return out
}

func routeIsPublic(pattern string) bool {
	for _, r := range routes {
		if r.pattern == pattern {
			return r.access == public
		}
	}
	return false
}

type serverInfo struct {
	APIVersion        int    `json:"api_version"`
	ServerVersion     string `json:"server_version"`
	BillingEnabled    bool   `json:"billing_enabled"`
	MaxBlobBytes      int64  `json:"max_blob_bytes"`
	DefaultQuotaBytes int64  `json:"default_quota_bytes"`
}

type health struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// NewRouter returns the server's HTTP handler. Protocol routes are under /v1; /healthz is at the root.
func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()
	h := &handlers{d: d, limiter: ratelimit.New(d.Clock),
		auth:  &auth.Service{DB: d.DB, Mailer: d.Mailer, Clock: d.Clock, Config: d.Config},
		blobs: &blobs.Service{DB: d.DB, Storage: d.Storage, Config: d.Config, Clock: d.Clock},
		items: &items.Service{DB: d.DB, Config: d.Config, Clock: d.Clock}}
	for _, rt := range routes {
		method, path, _ := strings.Cut(rt.pattern, " ")
		var handler http.Handler = rt.handler(h)
		if rt.access != public {
			handler = RequireSession(h.auth, rt.access == recoveryAllowed, h.fail, handler)
		}
		mux.Handle(method+" /v1"+path, handler)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = jsonEncode(w, health{Status: "ok", Version: d.Version})
	})
	return logx.Middleware(d.Log, cors(d.Config.AllowedOrigins, readDeadline(time.Minute, 30*time.Minute, mux)))
}

func (h *handlers) serverInfo(w http.ResponseWriter, r *http.Request) {
	quota := h.d.Config.QuotaBytes
	if h.d.Config.BillingEnabled {
		quota = plans.TrialQuota // what new accounts start with
	}
	httpx.WriteJSON(w, 200, serverInfo{1, h.d.Version, h.d.Config.BillingEnabled, h.d.Config.MaxBlobBytes, quota})
}
