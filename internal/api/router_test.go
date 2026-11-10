package api

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"feathernote/server/internal/config"
)

func testDeps() Deps {
	return Deps{Config: config.Config{MaxBlobBytes: 104857600, QuotaBytes: 10737418240}, Clock: time.Now,
		Log: slog.New(slog.NewJSONHandler(io.Discard, nil)), Version: "1.2.3"}
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	NewRouter(testDeps()).ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
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
	NewRouter(testDeps()).ServeHTTP(rec, httptest.NewRequest("POST", "/healthz", nil))
	if rec.Code != 405 {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestServerInfo(t *testing.T) {
	for _, billing := range []bool{true, false} {
		d := testDeps()
		d.Config.BillingEnabled = billing
		d.Config.QuotaBytes = 5000
		rec := httptest.NewRecorder()
		NewRouter(d).ServeHTTP(rec, httptest.NewRequest("GET", "/v1/server", nil))
		want := `{"api_version":1,"server_version":"1.2.3","billing_enabled":true,"max_blob_bytes":104857600,"default_quota_bytes":1073741824}`
		if !billing {
			want = `{"api_version":1,"server_version":"1.2.3","billing_enabled":false,"max_blob_bytes":104857600,"default_quota_bytes":5000}`
		}
		if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != want {
			t.Errorf("billing %v: %d %s", billing, rec.Code, rec.Body)
		}
	}
}
