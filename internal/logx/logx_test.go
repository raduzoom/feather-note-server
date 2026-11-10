package logx

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLogNeverContainsBodiesOrQueries(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := Middleware(log, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		SetIDs(r, "ACC1", "DEV1")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"title":"Groceries"}`))
	}))
	req := httptest.NewRequest("POST", "/v1/sync/pull?cursor=c_000000000001&limit=10&email=ana@example.com", strings.NewReader(`{"title":"Groceries"}`))
	h.ServeHTTP(httptest.NewRecorder(), req)
	line := buf.String()
	for _, want := range []string{`"method":"POST"`, `"path":"/v1/sync/pull"`, `"query":"limit=10"`, `"status":201`, `"account_id":"ACC1"`, `"device_id":"DEV1"`, `"duration_ms":`} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %s in %s", want, line)
		}
	}
	for _, never := range []string{"Groceries", "c_000000000001", "ana@example.com"} {
		if strings.Contains(line, never) {
			t.Errorf("log contains %q: %s", never, line)
		}
	}
}
