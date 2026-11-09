package main

import (
	"bytes"
	"net"
	"strings"
	"testing"
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
	var out, errb bytes.Buffer
	code := run([]string{"-addr", ln.Addr().String()}, &out, &errb)
	if code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errb.String(), "listen "+ln.Addr().String()) {
		t.Fatalf("stderr %q", errb.String())
	}
}
