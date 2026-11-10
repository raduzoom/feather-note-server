package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// SMTPConfig is any provider's submission server (Resend, Postmark, an ISP relay).
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

type smtpMailer struct{ cfg SMTPConfig }

// NewSMTP sends mail through an SMTP submission server. STARTTLS is used when offered and required for any
// host but localhost, so the password never crosses the network in clear.
func NewSMTP(cfg SMTPConfig) Mailer { return &smtpMailer{cfg} }

func (m *smtpMailer) SendCode(ctx context.Context, to, code string) error {
	return m.send(ctx, to, "Your Feather Cloud code", CodeText(code))
}

func (m *smtpMailer) SendAccountExists(ctx context.Context, to string) error {
	return m.send(ctx, to, "Your Feather Cloud account", ExistsText)
}

var errNoTLS = errors.New("smtp: the server offers no STARTTLS")

func (m *smtpMailer) send(ctx context.Context, to, subject, body string) error {
	if strings.ContainsAny(to, "\r\n") {
		return errors.New("smtp: invalid recipient")
	}
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	c, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	} else if m.cfg.Host != "localhost" {
		return errNoTLS
	}
	if m.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)); err != nil {
			return err
		}
	}
	from := m.cfg.From
	if a, err := mailAddress(from); err == nil {
		from = a
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n",
		m.cfg.From, to, subject, time.Now().UTC().Format(time.RFC1123Z), body)
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// mailAddress extracts the bare address from "Name <addr>".
func mailAddress(s string) (string, error) {
	i, j := strings.LastIndex(s, "<"), strings.LastIndex(s, ">")
	if i < 0 || j < i {
		return "", errors.New("no angle address")
	}
	return s[i+1 : j], nil
}
