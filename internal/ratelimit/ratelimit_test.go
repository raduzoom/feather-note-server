package ratelimit

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	l := New(func() time.Time { return now })
	for i := 0; i < 10; i++ {
		d := l.Allow(AuthIP, "1.1.1.1")
		if !d.Allowed || d.Limit != 10 || d.Remaining != 9-i {
			t.Fatalf("call %d: %+v", i, d)
		}
	}
	d := l.Allow(AuthIP, "1.1.1.1")
	if d.Allowed || d.Reset <= 0 {
		t.Fatalf("11th: %+v", d)
	}
	rec := httptest.NewRecorder()
	d.WriteHeaders(rec)
	if rec.Header().Get("Retry-After") == "" || rec.Header().Get("RateLimit-Limit") != "10" || rec.Header().Get("RateLimit-Remaining") != "0" || rec.Header().Get("RateLimit-Reset") == "" {
		t.Fatalf("headers %v", rec.Header())
	}
	if !l.Allow(AuthIP, "2.2.2.2").Allowed {
		t.Fatal("other key limited")
	}
	if !l.Allow(AuthEmail, "1.1.1.1").Allowed {
		t.Fatal("rules share counters")
	}
	now = now.Add(61 * time.Second)
	if !l.Allow(AuthIP, "1.1.1.1").Allowed {
		t.Fatal("window didn't reset")
	}
	ok := httptest.NewRecorder()
	l.Allow(Signup, "x").WriteHeaders(ok)
	if ok.Header().Get("Retry-After") != "" {
		t.Fatal("Retry-After on an allowed request")
	}
}

func TestRules(t *testing.T) {
	for _, c := range []struct {
		r Rule
		n int
		w time.Duration
	}{{Signup, 5, time.Hour}, {AuthIP, 10, time.Minute}, {AuthEmail, 10, time.Minute}, {Resend, 3, time.Hour},
		{VerifyIP, 10, time.Minute}, {CodeEmail, 5, time.Hour}, {ResendIP, 10, time.Minute}} {
		if c.r.Limit != c.n || c.r.Window != c.w {
			t.Errorf("%s: %+v", c.r.Name, c.r)
		}
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	if ClientIP(r, true, "") != "10.0.0.1" {
		t.Fatal("no header")
	}
	r.Header.Set("X-Forwarded-For", "1.1.1.1, 2.2.2.2")
	if ClientIP(r, true, "") != "2.2.2.2" || ClientIP(r, false, "") != "10.0.0.1" {
		t.Fatalf("%s %s", ClientIP(r, true, ""), ClientIP(r, false, ""))
	}
}

// Review Focus 3: behind Cloudflare every request comes from the tunnel; the visitor is in Cf-Connecting-Ip
func TestClientIPFromNamedHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.18.0.5:41000"
	r.Header.Set("Cf-Connecting-Ip", " 203.0.113.9 ")
	r.Header.Set("X-Forwarded-For", "198.51.100.7, 172.18.0.5")
	if got := ClientIP(r, true, "Cf-Connecting-Ip"); got != "203.0.113.9" {
		t.Fatalf("named header: %s", got)
	}
	r.Header.Del("Cf-Connecting-Ip")
	if got := ClientIP(r, true, "Cf-Connecting-Ip"); got != "172.18.0.5" {
		t.Fatalf("fallback to the last X-Forwarded-For hop: %s", got)
	}
	r.Header.Set("Cf-Connecting-Ip", "203.0.113.9")
	if got := ClientIP(r, false, "Cf-Connecting-Ip"); got != "172.18.0.5" {
		t.Fatalf("untrusted: %s", got)
	}
}

// One host usually holds a whole IPv6 /64, so per-IP limits count the /64.
func TestClientIPGroupsIPv6By64(t *testing.T) {
	ip := func(xff string) string {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Forwarded-For", xff)
		return ClientIP(r, true, "")
	}
	a, b := ip("2001:db8:1:2:3:4:5:6"), ip("2001:DB8:1:2:ffff::1")
	if a != b || a != "2001:db8:1:2::/64" {
		t.Fatalf("same /64: %q %q", a, b)
	}
	if ip("2001:db8:1:3::1") == a {
		t.Fatal("another /64 shares the key")
	}
	if got := ip("203.0.113.9"); got != "203.0.113.9" {
		t.Fatalf("IPv4: %q", got)
	}
	if got := ip("::ffff:203.0.113.9"); got != "203.0.113.9" {
		t.Fatalf("IPv4-mapped: %q", got)
	}
	if got := ip("not-an-ip"); got != "not-an-ip" {
		t.Fatalf("unparsable: %q", got)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "[2001:db8:9:9::1]:443"
	if got := ClientIP(r, false, ""); got != "2001:db8:9:9::/64" {
		t.Fatalf("peer address: %q", got)
	}
}

// S-F2: a flood of new keys can't grow memory without bound, however long the keys are.
func TestLimiterStaysBounded(t *testing.T) {
	now := time.Unix(1000, 0)
	l := New(func() time.Time { return now })
	long := strings.Repeat("x", 1000)
	for i := 0; i < 3*maxEntriesPerRule; i++ {
		l.Allow(Resend, long+strconv.Itoa(i))
	}
	if n := l.entries(); n > maxEntriesPerRule {
		t.Fatalf("%d entries for one rule, cap %d", n, maxEntriesPerRule)
	}
	// the newest keys are still counted
	key := long + strconv.Itoa(3*maxEntriesPerRule-1)
	for i := 0; i < 2; i++ {
		l.Allow(Resend, key)
	}
	if l.Allow(Resend, key).Allowed {
		t.Fatal("4th resend for a recent key allowed")
	}
}

// Expired windows go a few at a time on later calls, not in a full sweep under the lock.
func TestLimiterExpiresIncrementally(t *testing.T) {
	now := time.Unix(1000, 0)
	l := New(func() time.Time { return now })
	for i := 0; i < 50; i++ {
		l.Allow(AuthIP, strconv.Itoa(i))
	}
	now = now.Add(61 * time.Second)
	l.Allow(AuthIP, "new")
	if n := l.entries(); n != 1 {
		t.Fatalf("%d entries after every other window expired", n)
	}
}
