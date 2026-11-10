package api

import (
	"io"
	"net/http"
	"strconv"

	"feathernote/server/internal/blobs"
	"feathernote/server/internal/httpx"
)

func (h *handlers) putBlob(w http.ResponseWriter, r *http.Request) {
	sha := r.PathValue("sha256")
	if !blobs.ValidSHA(sha) {
		httpx.WriteError(w, httpx.InvalidRequest("The blob name must be 64 lowercase hex characters."))
		return
	}
	size, err := h.blobs.Put(r.Context(), SessionFrom(r.Context()).AccountID, sha, r.Body)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"sha256": sha, "size": size})
}

func (h *handlers) getBlob(w http.ResponseWriter, r *http.Request) {
	sha := r.PathValue("sha256")
	if !blobs.ValidSHA(sha) {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	body, size, err := h.blobs.Open(r.Context(), SessionFrom(r.Context()).AccountID, sha)
	if err != nil {
		h.fail(w, err)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(200)
	_, _ = io.Copy(w, body)
}
