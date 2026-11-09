// Package api holds the Feather Cloud HTTP handlers.
package api

import (
	"encoding/json"
	"net/http"
)

type health struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// NewRouter returns the server's HTTP handler.
func NewRouter(version string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(health{Status: "ok", Version: version})
	})
	return mux
}
