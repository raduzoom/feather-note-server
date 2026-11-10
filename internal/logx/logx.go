// Package logx is the request log. It records only what sync-protocol.md "Security notes" allows: method,
// path, the limit query value, status, duration, account id and device id. Never bodies, other query values,
// tokens, keys or emails.
package logx

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

type ids struct{ account, device string }

type key struct{}

// SetIDs records who made the request, for the log line written when it ends.
func SetIDs(r *http.Request, accountID, deviceID string) {
	if v, ok := r.Context().Value(key{}).(*ids); ok {
		v.account, v.device = accountID, deviceID
	}
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = 200
	}
	return r.ResponseWriter.Write(b)
}

// Middleware logs one line per request.
func Middleware(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		holder := &ids{}
		rec := &recorder{ResponseWriter: w}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), key{}, holder)))
		query := ""
		if limit := r.URL.Query().Get("limit"); limit != "" {
			if _, err := strconv.Atoi(limit); err == nil {
				query = "limit=" + limit
			}
		}
		if rec.status == 0 {
			rec.status = 200
		}
		log.Info("request", "method", r.Method, "path", r.URL.Path, "query", query, "status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(), "account_id", holder.account, "device_id", holder.device)
	})
}
