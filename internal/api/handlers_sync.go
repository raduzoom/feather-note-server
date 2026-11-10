package api

import (
	"net/http"

	"feathernote/server/internal/httpx"
	"feathernote/server/internal/items"
)

// a push carries at most 100 metas of 64 KiB, as base64
const pushBody = 10 << 20

func (h *handlers) push(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LibraryID string         `json:"library_id"`
		Changes   []items.Change `json:"changes"`
	}
	if err := httpx.Decode(r, &req, pushBody); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if req.Changes == nil {
		httpx.WriteError(w, httpx.InvalidRequest("changes is required."))
		return
	}
	results, err := h.items.Push(r.Context(), SessionFrom(r.Context()).AccountID, req.LibraryID, req.Changes)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"results": results})
}

func (h *handlers) pull(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	after, limit, err := items.ParsePull(q.Get("cursor"), q.Get("limit"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	page, err := h.items.Pull(r.Context(), SessionFrom(r.Context()).AccountID, q.Get("library_id"), after, limit)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, 200, page)
}
