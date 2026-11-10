// Package mailtest captures mail in tests.
package mailtest

import (
	"context"
	"errors"
	"sync"
)

// Capture records the last code and the number of notices per address.
type Capture struct {
	mu       sync.Mutex
	failNext int
	codes    map[string]string
	notices  map[string]int
}

func New() *Capture { return &Capture{codes: map[string]string{}, notices: map[string]int{}} }

// FailNext makes the next n sends fail, as a provider that is down would.
func (c *Capture) FailNext(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failNext = n
}

func (c *Capture) SendCode(_ context.Context, to, code string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failNext > 0 {
		c.failNext--
		return errors.New("smtp: 421 service not available")
	}
	c.codes[to] = code
	return nil
}

func (c *Capture) SendAccountExists(_ context.Context, to string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notices[to]++
	return nil
}

// LastCode for an address as typed or normalized (lowercase).
func (c *Capture) LastCode(to string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if code, ok := c.codes[to]; ok {
		return code
	}
	return c.codes[lower(to)]
}

func (c *Capture) Notices(to string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.notices[lower(to)]
}

func lower(s string) string {
	b := []byte(s)
	for i, ch := range b {
		if ch >= 'A' && ch <= 'Z' {
			b[i] = ch + 32
		}
	}
	return string(b)
}
