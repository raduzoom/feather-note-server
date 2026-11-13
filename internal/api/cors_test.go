package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

const appOrigin = "https://app.feathermark.com"

func corsDeps(origins ...string) Deps {
	d := testDeps()
	d.Config.AllowedOrigins = origins
	return d
}

func hasACHeader(h map[string][]string) bool {
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			return true
		}
	}
	return false
}

func TestPreflightFromAllowedOrigin(t *testing.T) {
	req := httptest.NewRequest("OPTIONS", "/v1/sync/push", nil)
	req.Header.Set("Origin", appOrigin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	rec := httptest.NewRecorder()
	NewRouter(corsDeps(appOrigin, "http://localhost:8431")).ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status %d", rec.Code)
	}
	for k, want := range map[string]string{
		"Access-Control-Allow-Origin":  appOrigin,
		"Access-Control-Allow-Methods": "GET, POST, PUT, DELETE",
		"Access-Control-Allow-Headers": "Authorization, Content-Type",
		"Access-Control-Max-Age":       "600",
		"Vary":                         "Origin",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body %q", rec.Body)
	}
}

func TestResponsesExposeRateLimitHeaders(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/server", nil)
	req.Header.Set("Origin", "http://localhost:8431")
	rec := httptest.NewRecorder()
	NewRouter(corsDeps(appOrigin, "http://localhost:8431")).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:8431" {
		t.Errorf("allow-origin %q", got)
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "Retry-After, RateLimit-Limit, RateLimit-Remaining, RateLimit-Reset" {
		t.Errorf("expose %q", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("vary %q", got)
	}
}

func TestOtherOriginsGetNoCORS(t *testing.T) {
	r := NewRouter(corsDeps(appOrigin))
	req := httptest.NewRequest("GET", "/v1/server", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 || hasACHeader(rec.Header()) {
		t.Errorf("simple request: %d %v", rec.Code, rec.Header())
	}
	pre := httptest.NewRequest("OPTIONS", "/v1/sync/push", nil)
	pre.Header.Set("Origin", "https://evil.example")
	pre.Header.Set("Access-Control-Request-Method", "POST")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, pre)
	if rec.Code != 403 || rec.Body.Len() != 0 || hasACHeader(rec.Header()) {
		t.Errorf("preflight: %d %q %v", rec.Code, rec.Body, rec.Header())
	}
}

func TestNoOriginBehavesAsToday(t *testing.T) {
	rec := httptest.NewRecorder()
	NewRouter(corsDeps(appOrigin)).ServeHTTP(rec, httptest.NewRequest("GET", "/v1/server", nil))
	if rec.Code != 200 || hasACHeader(rec.Header()) {
		t.Errorf("%d %v", rec.Code, rec.Header())
	}
}

func TestUnsetMeansNoCORS(t *testing.T) {
	for _, method := range []string{"GET", "OPTIONS"} {
		req := httptest.NewRequest(method, "/v1/server", nil)
		req.Header.Set("Origin", appOrigin)
		req.Header.Set("Access-Control-Request-Method", "GET")
		rec := httptest.NewRecorder()
		NewRouter(corsDeps()).ServeHTTP(rec, req)
		if hasACHeader(rec.Header()) || rec.Header().Get("Vary") != "" {
			t.Errorf("%s: %v", method, rec.Header())
		}
		if method == "OPTIONS" && rec.Code == 204 {
			t.Errorf("preflight answered with CORS disabled")
		}
	}
}

func TestNoCredentialsHeader(t *testing.T) {
	for _, method := range []string{"GET", "OPTIONS"} {
		req := httptest.NewRequest(method, "/v1/server", nil)
		req.Header.Set("Origin", appOrigin)
		req.Header.Set("Access-Control-Request-Method", "GET")
		rec := httptest.NewRecorder()
		NewRouter(corsDeps(appOrigin)).ServeHTTP(rec, req)
		if _, ok := rec.Header()["Access-Control-Allow-Credentials"]; ok {
			t.Errorf("%s sends Allow-Credentials", method)
		}
	}
}
