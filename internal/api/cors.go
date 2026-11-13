package api

import "net/http"

// cors lets browser apps on the listed origins call the API. The protocol uses a bearer token, never cookies,
// so Access-Control-Allow-Credentials is never sent. With no origins it returns next unchanged. A request with no
// Origin header (native apps, curl) passes through untouched; one from an unlisted origin gets no CORS headers,
// and its preflight gets 403.
func cors(origins []string, next http.Handler) http.Handler {
	if len(origins) == 0 {
		return next
	}
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Add("Vary", "Origin")
		preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
		if !allowed[origin] {
			if preflight {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		h.Set("Access-Control-Allow-Origin", origin)
		if preflight {
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.Set("Access-Control-Expose-Headers", "Retry-After, RateLimit-Limit, RateLimit-Remaining, RateLimit-Reset")
		next.ServeHTTP(w, r)
	})
}
