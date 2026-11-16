package main

import (
	"os"
	"regexp"
	"testing"
)

// Review I3, S-F9: the base stack publishes only the server, on this machine. The SeaweedFS filer has no login and
// accepts deletes on every path, so its ports are published only by the opt-in debug file, and only to 127.0.0.1.
// With FEATHER_TRUST_PROXY=true a client reaching 8420 directly could fake its address, so 8420 stays local too.
func TestComposePublishesOnlyToLocalhost(t *testing.T) {
	base := published(t, "../../deploy/docker-compose.yml")
	if len(base) != 1 || base[0] != "127.0.0.1:8420:8420" {
		t.Fatalf("the base file publishes %v, want only 127.0.0.1:8420:8420", base)
	}
	debug := published(t, "../../deploy/docker-compose.debug.yml")
	if len(debug) != 2 {
		t.Fatalf("the debug file publishes %v, want the S3 API and the filer UI", debug)
	}
	for _, p := range debug {
		if !regexp.MustCompile(`^127\.0\.0\.1:\d+:\d+$`).MatchString(p) {
			t.Errorf("debug port %q is published beyond this machine", p)
		}
	}
}

func published(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range regexp.MustCompile(`(?m)^\s+- "([^"]+)"\s*$`).FindAllStringSubmatch(string(data), -1) {
		if regexp.MustCompile(`^(\d+\.\d+\.\d+\.\d+:)?\d+:\d+$`).MatchString(m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

// S-F9: no secret falls back to a value printed in this public repo; compose refuses to start without it.
func TestComposeHasNoPublicSecrets(t *testing.T) {
	for _, path := range []string{"../../deploy/docker-compose.yml", "../../deploy/docker-compose.test.yml"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if m := regexp.MustCompile(`\$\{(POSTGRES_PASSWORD|S3_SECRET_KEY|FEATHER_SERVER_SECRET):-[^}]*\}`).FindString(string(data)); m != "" {
			t.Errorf("%s: %s falls back to a public value", path, m)
		}
	}
	data, err := os.ReadFile("../../deploy/.env.example")
	if err != nil {
		t.Fatal(err)
	}
	if m := regexp.MustCompile(`(?m)^(POSTGRES_PASSWORD|S3_SECRET_KEY|FEATHER_SERVER_SECRET)=\S+`).FindString(string(data)); m != "" {
		t.Errorf(".env.example ships a secret: %s", m)
	}
}
