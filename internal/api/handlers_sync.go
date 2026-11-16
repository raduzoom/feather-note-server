package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"feathernote/server/internal/httpx"
	"feathernote/server/internal/items"
)

// a push carries at most 100 metas of 64 KiB, as base64
const pushBody = 10 << 20

// pullWriteTime is how long a pull response may take to send; a client that stops reading can't hold it longer.
const pullWriteTime = 2 * time.Minute

type pushRequest struct {
	LibraryID string     `json:"library_id"`
	Changes   changeList `json:"changes"`
}

// changeList decodes at most items.MaxChanges+1 changes and skips the rest, so a body of millions of tiny changes
// never becomes millions of structs; the one extra lets items.Push refuse the push with its own message.
type changeList []items.Change

func (l *changeList) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		*l = nil
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('[') {
		return errors.New("changes must be an array")
	}
	out := changeList{}
	for dec.More() && len(out) <= items.MaxChanges {
		var c items.Change
		if err := dec.Decode(&c); err != nil {
			return err
		}
		out = append(out, c)
	}
	*l = out
	return nil
}

func (h *handlers) push(w http.ResponseWriter, r *http.Request) {
	var req pushRequest
	if err := httpx.Decode(r, &req, pushBody); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if req.Changes == nil {
		httpx.WriteError(w, httpx.InvalidRequest("changes is required."))
		return
	}
	results, err := h.items.Push(r.Context(), SessionFrom(r.Context()).AccountID, req.LibraryID, []items.Change(req.Changes))
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
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(pullWriteTime)) // not every writer supports it (tests)
	httpx.WriteJSON(w, 200, page)
}
