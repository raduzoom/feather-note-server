// Package ratelimit implements the limits in sync-protocol.md "Limits": fixed windows, in memory, per process.
package ratelimit

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rule is one limit.
type Rule struct {
	Name   string
	Limit  int
	Window time.Duration
}

var (
	Signup    = Rule{"signup", 5, time.Hour}        // per IP
	AuthIP    = Rule{"auth-ip", 10, time.Minute}    // prelogin, login, recover, per IP
	AuthEmail = Rule{"auth-email", 10, time.Minute} // the same, per email
	Resend    = Rule{"resend", 3, time.Hour}        // per email
)

// Decision is the outcome of one request against one rule.
type Decision struct {
	Allowed   bool
	Limit     int
	Remaining int
	Reset     time.Duration
}

// WriteHeaders adds RateLimit-Limit, -Remaining, -Reset (seconds), and Retry-After when refused.
func (d Decision) WriteHeaders(w http.ResponseWriter) {
	secs := strconv.Itoa(int(math.Ceil(d.Reset.Seconds())))
	w.Header().Set("RateLimit-Limit", strconv.Itoa(d.Limit))
	w.Header().Set("RateLimit-Remaining", strconv.Itoa(d.Remaining))
	w.Header().Set("RateLimit-Reset", secs)
	if !d.Allowed {
		w.Header().Set("Retry-After", secs)
	}
}

type window struct {
	start time.Time
	count int
}

// Limiter counts requests per rule and key.
type Limiter struct {
	mu    sync.Mutex
	clock func() time.Time
	wins  map[string]*window
	calls int
}

func New(clock func() time.Time) *Limiter {
	return &Limiter{clock: clock, wins: map[string]*window{}}
}

// Allow counts one request.
func (l *Limiter) Allow(rule Rule, key string) Decision {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	if l.calls++; l.calls%1000 == 0 {
		for k, w := range l.wins {
			if now.Sub(w.start) >= time.Hour {
				delete(l.wins, k)
			}
		}
	}
	k := rule.Name + "\x00" + key
	w := l.wins[k]
	if w == nil || now.Sub(w.start) >= rule.Window {
		w = &window{start: now}
		l.wins[k] = w
	}
	reset := rule.Window - now.Sub(w.start)
	if w.count >= rule.Limit {
		return Decision{false, rule.Limit, 0, reset}
	}
	w.count++
	return Decision{true, rule.Limit, rule.Limit - w.count, reset}
}

// ClientIP is the visitor's address. Behind a trusted proxy: the named header when set and present (Cloudflare's
// Cf-Connecting-Ip), else the last X-Forwarded-For hop. Otherwise the peer address.
func ClientIP(r *http.Request, trustProxy bool, header string) string {
	if trustProxy && header != "" {
		if v := strings.TrimSpace(r.Header.Get(header)); v != "" {
			return v
		}
	}
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			hops := strings.Split(xff, ",")
			return strings.TrimSpace(hops[len(hops)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
