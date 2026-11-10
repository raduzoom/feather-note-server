package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"feathernote/server/internal/auth"
	"feathernote/server/internal/httpx"
	"feathernote/server/internal/logx"
)

type sessionKey struct{}

// SessionFrom is the caller's session, set by RequireSession.
func SessionFrom(ctx context.Context) auth.Session {
	s, _ := ctx.Value(sessionKey{}).(auth.Session)
	return s
}

// RequireSession lets a request through with a valid bearer token. A recovery-scope session reaches only
// routes that allow it; elsewhere it is 403 forbidden_scope.
func RequireSession(svc *auth.Service, allowRecovery bool, fail func(http.ResponseWriter, error), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			httpx.WriteError(w, httpx.ErrUnauthorized)
			return
		}
		sess, err := svc.Authenticate(r.Context(), token)
		if err != nil {
			fail(w, err)
			return
		}
		logx.SetIDs(r, sess.AccountID, sess.DeviceID)
		if sess.Scope != "full" && !allowRecovery {
			httpx.WriteError(w, httpx.ErrForbiddenScope)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess)))
	})
}

// readDeadline bounds how long a request body may take: json for ordinary requests, blob for blob uploads
// (up to max_blob_bytes on a slow line). A body still arriving after it fails to read.
func readDeadline(json, blob time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d := json
		if strings.HasPrefix(r.URL.Path, "/v1/blobs/") {
			d = blob
		}
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(d))
		next.ServeHTTP(w, r)
	})
}
