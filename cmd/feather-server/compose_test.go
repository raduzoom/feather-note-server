package main

import (
	"os"
	"regexp"
	"testing"
)

// Review I3: the stack publishes nothing beyond this machine. The SeaweedFS filer UI has no login, and with
// FEATHER_TRUST_PROXY=true a client reaching 8420 directly could fake its address; only the reverse proxy on
// the same host should reach them.
func TestComposePublishesOnlyToLocalhost(t *testing.T) {
	data, err := os.ReadFile("../../deploy/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	ports := regexp.MustCompile(`(?m)^\s+- "([^"]+)"\s*$`).FindAllStringSubmatch(string(data), -1)
	n := 0
	for _, m := range ports {
		if !regexp.MustCompile(`^\d+:\d+$|^127\.0\.0\.1:\d+:\d+$`).MatchString(m[1]) {
			continue
		}
		n++
		if !regexp.MustCompile(`^127\.0\.0\.1:`).MatchString(m[1]) {
			t.Errorf("port %q is published on every interface", m[1])
		}
	}
	if n != 3 {
		t.Fatalf("expected 3 published ports, found %d", n)
	}
}
