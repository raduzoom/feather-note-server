package api

import (
	"net/http"

	"feathernote/server/internal/auth"
	"feathernote/server/internal/httpx"
	"feathernote/server/internal/ratelimit"
)

type credentials struct {
	Email           string    `json:"email"`
	AuthKey         httpx.B64 `json:"auth_key"`
	RecoveryAuthKey httpx.B64 `json:"recovery_auth_key"`
	DeviceName      string    `json:"device_name"`
}

// authRequest decodes the body, normalizes the email and applies the per-IP and per-email limits.
func (h *handlers) authRequest(w http.ResponseWriter, r *http.Request) (credentials, string, bool) {
	var req credentials
	if err := httpx.Decode(r, &req, smallBody); err != nil {
		httpx.WriteError(w, err)
		return req, "", false
	}
	email, err := auth.NormalizeEmail(req.Email)
	if err != nil {
		httpx.WriteError(w, httpx.InvalidRequest("Invalid email."))
		return req, "", false
	}
	ip := h.ip(r)
	ok := h.limit(w, func() ratelimit.Decision { return h.limiter.Allow(ratelimit.AuthIP, ip) },
		func() ratelimit.Decision { return h.limiter.Allow(ratelimit.AuthEmail, email) })
	return req, email, ok
}

func (h *handlers) prelogin(w http.ResponseWriter, r *http.Request) {
	_, email, ok := h.authRequest(w, r)
	if !ok {
		return
	}
	kdf, salt, err := h.auth.Prelogin(r.Context(), email)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"kdf": kdf, "salt": httpx.B64(salt)})
}

func (h *handlers) login(w http.ResponseWriter, r *http.Request) {
	req, email, ok := h.authRequest(w, r)
	if !ok {
		return
	}
	sess, account, wrapped, err := h.auth.Login(r.Context(), email, req.AuthKey, req.DeviceName)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"token": sess.Token, "device_id": sess.DeviceID, "account": account,
		"wrapped_master_password": httpx.B64(wrapped)})
}

func (h *handlers) recover(w http.ResponseWriter, r *http.Request) {
	req, email, ok := h.authRequest(w, r)
	if !ok {
		return
	}
	sess, wrapped, err := h.auth.Recover(r.Context(), email, req.RecoveryAuthKey, req.DeviceName)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"token": sess.Token, "device_id": sess.DeviceID, "wrapped_master_recovery": httpx.B64(wrapped)})
}

func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.auth.Logout(r.Context(), SessionFrom(r.Context())); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) devices(w http.ResponseWriter, r *http.Request) {
	list, err := h.auth.Devices(r.Context(), SessionFrom(r.Context()))
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"devices": list})
}

func (h *handlers) revokeDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("device_id")
	if !auth.ValidID(id) {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	if err := h.auth.RevokeDevice(r.Context(), SessionFrom(r.Context()), id); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
