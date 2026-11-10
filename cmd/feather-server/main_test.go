package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-version"}, &out, &errb); code != 0 {
		t.Fatalf("code %d, stderr %s", code, errb.String())
	}
	if out.String() != "feather-server dev\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestRunFailsWhenPortTaken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	t.Setenv("FEATHER_DATABASE_URL", "postgres://unused")
	t.Setenv("FEATHER_SERVER_SECRET", strings.Repeat("01", 32))
	var out, errb bytes.Buffer
	code := run([]string{"-addr", ln.Addr().String()}, &out, &errb)
	if code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errb.String(), "listen "+ln.Addr().String()) {
		t.Fatalf("stderr %q", errb.String())
	}
}

func TestHealthcheckFlag(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(404)
		}
	}))
	defer ok.Close()
	var out, errb bytes.Buffer
	if code := run([]string{"-healthcheck", "-addr", strings.TrimPrefix(ok.URL, "http://")}, &out, &errb); code != 0 {
		t.Fatalf("healthy: %d %s", code, errb.String())
	}
	ok.Close()
	if code := run([]string{"-healthcheck", "-addr", strings.TrimPrefix(ok.URL, "http://")}, &out, &errb); code != 1 {
		t.Fatalf("down: %d", code)
	}
}

// Review I2: slow clients can't hold connections open forever.
func TestHTTPServerHasTimeouts(t *testing.T) {
	s := newHTTPServer(http.NotFoundHandler())
	if s.ReadHeaderTimeout != 10*time.Second || s.IdleTimeout != 120*time.Second || s.MaxHeaderBytes != 64<<10 {
		t.Fatalf("%+v", s)
	}
}
