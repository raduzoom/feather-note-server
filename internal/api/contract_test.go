//go:build integration

package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"
)

// contract validates every response the integration tests receive against the OpenAPI file, and records
// which operations were seen.
type contract struct {
	v        validator.Validator
	mu       sync.Mutex
	failures []string
	seen     map[string]bool
}

var theContract *contract

var knownSpecGap = map[string]bool{"GET /devices": true, "DELETE /devices/{device_id}": true, "GET /sync/pull": true, "GET /blobs/{sha256}": true}

func (c *contract) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return res, err
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	res.Body = io.NopCloser(bytes.NewReader(body))
	check := *res
	check.Body = io.NopCloser(bytes.NewReader(body))
	path := strings.TrimPrefix(req.URL.Path, "/v1")
	if strings.HasPrefix(req.URL.Path, "/v1/") {
		ok, errs := c.v.ValidateHttpResponse(req, &check)
		// TODO(spec): sync-protocol.md gives a recovery-scope token 403 forbidden_scope on every route but
		// set-password and logout; the OpenAPI file doesn't list 403 for these four. Remove once it does.
		if res.StatusCode == 403 && knownSpecGap[req.Method+" "+pattern(path)] && bytes.Contains(body, []byte(`"forbidden_scope"`)) {
			ok, errs = true, nil
		}
		c.mu.Lock()
		c.seen[req.Method+" "+pattern(path)] = true
		if !ok {
			for _, e := range errs {
				detail := ""
				for _, se := range e.SchemaValidationErrors {
					detail += " | " + se.FieldPath + " " + se.KeywordLocation + ": " + se.Reason
				}
				if len(detail) > 600 {
					detail = detail[:600]
				}
				c.failures = append(c.failures, fmt.Sprintf("%s %s → %d: %s (%s)%s\n    body: %.300s", req.Method, path, res.StatusCode, e.Message, e.Reason, detail, body))
			}
		}
		c.mu.Unlock()
	}
	return res, nil
}

// pattern maps a concrete path back to its route pattern.
func pattern(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) == 3 && parts[1] == "devices" {
		return "/devices/{device_id}"
	}
	if len(parts) == 3 && parts[1] == "blobs" {
		return "/blobs/{sha256}"
	}
	return path
}

func TestMain(m *testing.M) {
	data, err := os.ReadFile(specPath(nil))
	if err != nil {
		fmt.Println("OpenAPI file not found:", specPath(nil))
		os.Exit(1)
	}
	doc, err := libopenapi.NewDocument(data)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	v, errs := validator.NewValidator(doc)
	if len(errs) > 0 {
		fmt.Println(errs)
		os.Exit(1)
	}
	theContract = &contract{v: v, seen: map[string]bool{}}
	testClient.Transport = theContract
	code := m.Run()
	if code == 0 && len(theContract.failures) > 0 {
		fmt.Println("responses that don't match sync-protocol.openapi.yaml:")
		for _, f := range theContract.failures {
			fmt.Println("  " + f)
		}
		code = 1
	}
	if code == 0 && os.Getenv("FEATHER_CONTRACT_PARTIAL") == "" {
		for _, r := range Routes() {
			if !theContract.seen[r] {
				fmt.Println("no test called", r)
				code = 1
			}
		}
	}
	os.Exit(code)
}

func TestResponsesMatchContract(t *testing.T) {
	if theContract == nil || testClient.Transport != theContract {
		t.Fatal("the contract transport is not installed")
	}
	e := newEnv(t)
	if r := e.do(request{method: "GET", path: "/v1/server"}); r.status != 200 {
		t.Fatal(r.status)
	}
}
