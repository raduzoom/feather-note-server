// Package ratelimit implements the limits in sync-protocol.md "Limits": fixed windows, in memory, per process.
package ratelimit

import (
	"crypto/sha256"
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
	VerifyIP  = Rule{"verify-ip", 10, time.Minute}  // per IP
	CodeEmail = Rule{"code-email", 5, time.Hour}    // signup and resend together, per email: codes and notices
	ResendIP  = Rule{"resend-ip", 10, time.Minute}  // per IP, ahead of the per-email rules
)

// maxEntriesPerRule caps each rule's counters. A flood of new keys pushes out that rule's oldest windows first, so
// memory stays bounded and the keys seen most recently keep their counts.
const maxEntriesPerRule = 50_000

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

// opened records when a key's window started, in the order windows were opened.
type opened struct {
	key   [32]byte
	start time.Time
}

// ruleState holds one rule's windows. Every window of a rule has the same length, so the order they were opened in is
// also the order they expire in: expiry only ever looks at the front of the queue.
type ruleState struct {
	wins  map[[32]byte]*window
	queue []opened
	head  int
}

// pop takes the oldest entry off the queue and drops its window, if that is still the key's current one.
func (r *ruleState) pop() {
	q := r.queue[r.head]
	r.head++
	if r.head > len(r.queue)/2 { // keep the queue's memory proportional to what it holds
		r.queue = append(r.queue[:0], r.queue[r.head:]...)
		r.head = 0
	}
	if w := r.wins[q.key]; w != nil && w.start.Equal(q.start) {
		delete(r.wins, q.key)
	}
}

// Limiter counts requests per rule and key. Keys are stored as SHA-256, so a long key costs no more than a short one.
type Limiter struct {
	mu    sync.Mutex
	clock func() time.Time
	rules map[string]*ruleState
}

func New(clock func() time.Time) *Limiter {
	return &Limiter{clock: clock, rules: map[string]*ruleState{}}
}

// entries is how many windows are kept, over all rules.
func (l *Limiter) entries() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, r := range l.rules {
		n += len(r.wins)
	}
	return n
}

// Allow counts one request.
func (l *Limiter) Allow(rule Rule, key string) Decision {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	r := l.rules[rule.Name]
	if r == nil {
		r = &ruleState{wins: map[[32]byte]*window{}}
		l.rules[rule.Name] = r
	}
	for r.head < len(r.queue) && now.Sub(r.queue[r.head].start) >= rule.Window {
		r.pop()
	}
	k := sha256.Sum256([]byte(key))
	w := r.wins[k]
	if w == nil || now.Sub(w.start) >= rule.Window {
		for w == nil && len(r.wins) >= maxEntriesPerRule && r.head < len(r.queue) {
			r.pop() // the rule's oldest window makes room
		}
		w = &window{start: now}
		r.wins[k] = w
		r.queue = append(r.queue, opened{k, now})
	}
	reset := rule.Window - now.Sub(w.start)
	if w.count >= rule.Limit {
		return Decision{false, rule.Limit, 0, reset}
	}
	w.count++
	return Decision{true, rule.Limit, rule.Limit - w.count, reset}
}

// ClientIP is the visitor's address as a rate-limit key. Behind a trusted proxy: the named header when set and
// present (Cloudflare's Cf-Connecting-Ip), else the last X-Forwarded-For hop. Otherwise the peer address. An IPv6
// address counts as its /64, since one host usually holds a whole /64.
func ClientIP(r *http.Request, trustProxy bool, header string) string {
	if trustProxy && header != "" {
		if v := strings.TrimSpace(r.Header.Get(header)); v != "" {
			return group(v)
		}
	}
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			hops := strings.Split(xff, ",")
			return group(strings.TrimSpace(hops[len(hops)-1]))
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return group(r.RemoteAddr)
	}
	return group(host)
}

func group(addr string) string {
	ip := net.ParseIP(addr)
	if ip == nil {
		return addr
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}
