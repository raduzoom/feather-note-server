// Package mail sends the verification code and the "account exists" notice.
package mail

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// Mailer sends the two emails signup needs.
type Mailer interface {
	SendCode(ctx context.Context, to, code string) error
	SendAccountExists(ctx context.Context, to string) error
}

// CodeText and ExistsText are the emails' bodies.
func CodeText(code string) string {
	return fmt.Sprintf("Your Feather Cloud code is %s. It expires in 15 minutes.", code)
}

const ExistsText = "Someone tried to create a Feather Cloud account with this email address, which already has one. Sign in instead. If this wasn't you, you can ignore this email."

type logMailer struct {
	mu sync.Mutex
	w  io.Writer
}

// NewLog writes each email to w instead of sending it: for development, and for self-hosters without an email
// provider, who read codes from the server's output. It is separate from the request log, which never holds
// emails or codes.
func NewLog(w io.Writer) Mailer { return &logMailer{w: w} }

func (m *logMailer) SendCode(_ context.Context, to, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := fmt.Fprintf(m.w, "mail to %s: %s\n", to, CodeText(code))
	return err
}

func (m *logMailer) SendAccountExists(_ context.Context, to string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := fmt.Fprintf(m.w, "mail to %s: %s\n", to, ExistsText)
	return err
}
