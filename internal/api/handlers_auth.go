package api

import (
	"net/http"

	"feathernote/server/internal/auth"
	"feathernote/server/internal/httpx"
	"feathernote/server/internal/ratelimit"
)

const smallBody = 64 << 10

// limit checks rules in order, writes the headers of the tightest one, and reports whether to go on.
func (h *handlers) limit(w http.ResponseWriter, checks ...func() ratelimit.Decision) bool {
	var tightest *ratelimit.Decision
	for _, check := range checks {
		d := check()
		if tightest == nil || !d.Allowed || (tightest.Allowed && d.Remaining < tightest.Remaining) {
			tightest = &d
		}
		if !d.Allowed {
			break
		}
	}
	tightest.WriteHeaders(w)
	if !tightest.Allowed {
		httpx.WriteError(w, httpx.ErrRateLimited)
		return false
	}
	return true
}

func (h *handlers) ip(r *http.Request) string {
	return ratelimit.ClientIP(r, h.d.Config.TrustProxy, h.d.Config.ClientIPHeader)
}

func (h *handlers) signup(w http.ResponseWriter, r *http.Request) {
	ip := h.ip(r)
	if !h.limit(w, func() ratelimit.Decision { return h.limiter.Allow(ratelimit.Signup, ip) }) {
		return
	}
	var req auth.SignupRequest
	if err := httpx.Decode(r, &req, smallBody); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.auth.Signup(r.Context(), req); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (h *handlers) verify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email      string `json:"email"`
		Code       string `json:"code"`
		DeviceName string `json:"device_name"`
	}
	if err := httpx.Decode(r, &req, smallBody); err != nil {
		httpx.WriteError(w, err)
		return
	}
	session, account, err := h.auth.Verify(r.Context(), req.Email, req.Code, req.DeviceName)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"token": session.Token, "device_id": session.DeviceID, "account": account})
}

func (h *handlers) resend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := httpx.Decode(r, &req, smallBody); err != nil {
		httpx.WriteError(w, err)
		return
	}
	email, err := auth.NormalizeEmail(req.Email)
	if err != nil {
		httpx.WriteError(w, httpx.InvalidRequest("Invalid email."))
		return
	}
	if !h.limit(w, func() ratelimit.Decision { return h.limiter.Allow(ratelimit.Resend, email) }) {
		return
	}
	if err := h.auth.Resend(r.Context(), email); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
