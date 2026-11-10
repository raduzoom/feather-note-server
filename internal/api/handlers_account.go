package api

import (
	"net/http"

	"feathernote/server/internal/auth"
	"feathernote/server/internal/httpx"
	"feathernote/server/internal/plans"
)

func (h *handlers) getAccount(w http.ResponseWriter, r *http.Request) {
	account, err := plans.Current(r.Context(), h.d.DB, SessionFrom(r.Context()).AccountID, h.d.Config, h.d.Clock())
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, 200, account)
}

func (h *handlers) setPassword(w http.ResponseWriter, r *http.Request) {
	var req auth.SetPasswordRequest
	if err := httpx.Decode(r, &req, smallBody); err != nil {
		httpx.WriteError(w, err)
		return
	}
	token, err := h.auth.SetPassword(r.Context(), SessionFrom(r.Context()), req)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"token": token})
}

func (h *handlers) deleteAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AuthKey httpx.B64 `json:"auth_key"`
	}
	if err := httpx.Decode(r, &req, smallBody); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.auth.DeleteAccount(r.Context(), SessionFrom(r.Context()), req.AuthKey); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
