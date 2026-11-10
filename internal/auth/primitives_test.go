package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"regexp"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	if n, err := NormalizeEmail("  Ana@Example.COM "); err != nil || n != "ana@example.com" {
		t.Fatalf("%q %v", n, err)
	}
	a, _ := NormalizeEmail("Éva@x.io")
	b, _ := NormalizeEmail("Éva@x.io")
	if a != b || a != "éva@x.io" {
		t.Fatalf("%q %q", a, b)
	}
	for _, bad := range []string{"no-at", "@x", "a@", "a@b@c", ""} {
		if _, err := NormalizeEmail(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestTokens(t *testing.T) {
	tok, hash := NewToken()
	if !regexp.MustCompile(`^fn1_[A-Za-z0-9_-]{43}$`).MatchString(tok) {
		t.Fatalf("token %q", tok)
	}
	tok2, _ := NewToken()
	if tok == tok2 {
		t.Fatal("tokens repeat")
	}
	if h, ok := HashToken(tok); !ok || !bytes.Equal(h, hash) {
		t.Fatal("hash differs")
	}
	if _, ok := HashToken("fn1_short"); ok {
		t.Fatal("short token accepted")
	}
	if _, ok := HashToken("fn2_" + tok[4:]); ok {
		t.Fatal("wrong prefix accepted")
	}
}

func TestCodes(t *testing.T) {
	re := regexp.MustCompile(`^[0-9]{6}$`)
	low := false
	for i := 0; i < 1000; i++ {
		c, h := NewCode()
		if !re.MatchString(c) || !bytes.Equal(h, Hash([]byte(c))) {
			t.Fatalf("code %q", c)
		}
		low = low || c < "100000"
	}
	if !low {
		t.Fatal("no code below 100000 in 1,000: leading zeros missing")
	}
}

func TestFakeSalt(t *testing.T) {
	secret := bytes.Repeat([]byte{1}, 32)
	s := FakeSalt(secret, "ana@example.com")
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("ana@example.com"))
	if !bytes.Equal(s, m.Sum(nil)[:16]) {
		t.Fatalf("salt %x", s)
	}
	if bytes.Equal(s, FakeSalt(secret, "bo@example.com")) || bytes.Equal(s, FakeSalt(bytes.Repeat([]byte{2}, 32), "ana@example.com")) {
		t.Fatal("salt doesn't depend on email and secret")
	}
	if !Equal(s, FakeSalt(secret, "ana@example.com")) || Equal(s, s[:15]) {
		t.Fatal("Equal")
	}
	if len(NewID()) != 26 {
		t.Fatal("id")
	}
}
