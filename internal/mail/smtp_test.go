package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
)

// fakeSMTP accepts one message on 127.0.0.1 and returns what it received.
func fakeSMTP(t *testing.T) (addr string, got chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got = make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		w := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
		var transcript strings.Builder
		w("220 fake ESMTP")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			transcript.WriteString(line)
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				w("250-fake")
				w("250 AUTH PLAIN")
			case strings.HasPrefix(cmd, "AUTH"):
				w("235 ok")
			case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
				w("250 ok")
			case cmd == "DATA":
				w("354 go")
				for {
					l, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if l == ".\r\n" {
						break
					}
					transcript.WriteString(l)
				}
				w("250 queued")
			case cmd == "QUIT":
				w("221 bye")
				got <- transcript.String()
				return
			default:
				w("250 ok")
			}
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String(), got
}

func TestSMTPDelivers(t *testing.T) {
	addr, got := fakeSMTP(t)
	host, portText, _ := net.SplitHostPort(addr)
	port := 0
	for _, c := range portText {
		port = port*10 + int(c-'0')
	}
	if host == "127.0.0.1" {
		host = "localhost"
	}
	m := NewSMTP(SMTPConfig{Host: host, Port: port, Username: "u", Password: "p", From: "Feather Cloud <codes@example.com>"})
	if err := m.SendCode(context.Background(), "ana@example.com", "123456"); err != nil {
		t.Fatal(err)
	}
	text := <-got
	for _, want := range []string{"RCPT TO:<ana@example.com>", "From: Feather Cloud <codes@example.com>", "To: ana@example.com",
		"Subject: Your Feather Cloud code", "Your Feather Cloud code is 123456. It expires in 15 minutes.", "Content-Type: text/plain; charset=utf-8"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

func TestSMTPRefusesPlainTextToRemoteHosts(t *testing.T) {
	addr, _ := fakeSMTP(t)
	_, portText, _ := net.SplitHostPort(addr)
	port := 0
	for _, c := range portText {
		port = port*10 + int(c-'0')
	}
	// 127.0.0.1 is not the name "localhost": without STARTTLS the password must not be sent
	m := NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: port, Username: "u", Password: "p", From: "codes@example.com"})
	if err := m.SendCode(context.Background(), "ana@example.com", "123456"); err == nil {
		t.Fatal("sent credentials without TLS")
	}
}
