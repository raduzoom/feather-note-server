package mail

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// S-F6: a normal address prints as it always has (scripts grep "mail to <email>"); one with a control character is
// quoted, so it can't start a forged line.
func TestLogMailerKeepsOneLinePerMail(t *testing.T) {
	var out bytes.Buffer
	m := NewLog(&out)
	_ = m.SendCode(context.Background(), "ana@example.com", "123456")
	if !strings.HasPrefix(out.String(), "mail to ana@example.com: ") {
		t.Fatalf("normal address: %q", out.String())
	}
	for _, to := range []string{"zz\nmail to victim@example.com: your code is 000000", "a\r@x.io", "a@x\x1b[2J.io", "a b@x.io"} {
		out.Reset()
		_ = m.SendCode(context.Background(), to, "123456")
		_ = m.SendAccountExists(context.Background(), to)
		if s := out.String(); strings.Count(s, "\n") != 2 || strings.ContainsAny(s, "\r\x1b ") {
			t.Errorf("%q printed as %q", to, s)
		}
	}
}
