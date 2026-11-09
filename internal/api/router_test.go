package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	NewRouter("1.2.3").ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type %q", ct)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"status":"ok","version":"1.2.3"}` {
		t.Fatalf("body %s", got)
	}
}

func TestHealthzRejectsPost(t *testing.T) {
	rec := httptest.NewRecorder()
	NewRouter("x").ServeHTTP(rec, httptest.NewRequest("POST", "/healthz", nil))
	if rec.Code != 405 {
		t.Fatalf("status %d", rec.Code)
	}
}
