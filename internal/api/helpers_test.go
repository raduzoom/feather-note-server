//go:build integration

package api

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"feathernote/server/internal/auth"
	"feathernote/server/internal/blobs"
	"feathernote/server/internal/blobs/blobstest"
	"feathernote/server/internal/config"
	"feathernote/server/internal/mail/mailtest"
	"feathernote/server/internal/store/storetest"
)

// env is one server on a fresh database, with a capturing mailer and a clock tests can move.
type env struct {
	t       *testing.T
	srv     *httptest.Server
	db      *pgxpool.Pool
	mail    *mailtest.Capture
	cfg     config.Config
	mu      sync.Mutex
	now     time.Time
	logs    *bytes.Buffer
	ip      int
	storage blobs.Storage
}

func newEnv(t *testing.T, configure ...func(*config.Config)) *env {
	t.Helper()
	cfg := config.Config{ServerSecret: bytes.Repeat([]byte{7}, 32), BillingEnabled: true, QuotaBytes: 10737418240,
		MaxBlobBytes: 104857600, Mail: "log", TrustProxy: true}
	for _, f := range configure {
		f(&cfg)
	}
	e := &env{t: t, db: storetest.NewDB(t), mail: mailtest.New(), cfg: cfg, now: time.Date(2026, 11, 9, 14, 32, 0, 123e6, time.UTC), logs: &bytes.Buffer{}}
	e.storage, e.cfg.S3 = blobstest.NewStorage(t)
	e.srv = httptest.NewServer(NewRouter(e.deps()))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

type logWriter struct{ e *env }

func (w logWriter) Write(p []byte) (int, error) {
	w.e.mu.Lock()
	defer w.e.mu.Unlock()
	return w.e.logs.Write(p)
}

func (e *env) deps() Deps {
	return Deps{Config: e.cfg, DB: e.db, Mailer: e.mail, Storage: e.storage, Clock: e.clock,
		Log: slog.New(slog.NewJSONHandler(logWriter{e}, nil)), Version: "test"}
}

// request is one call; ip "" gets a fresh address so per-IP limits don't leak between steps.
type request struct {
	method, path, token, ip string
	body                    any
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (r response) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("not JSON (%d): %s", r.status, r.body)
	}
	return m
}

func (r response) code() string {
	var m struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(r.body, &m)
	return m.Error.Code
}

func (e *env) do(r request) response {
	e.t.Helper()
	var body io.Reader
	switch b := r.body.(type) {
	case nil:
	case []byte:
		body = bytes.NewReader(b)
	case string:
		body = bytes.NewReader([]byte(b))
	default:
		data, _ := json.Marshal(b)
		body = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(r.method, e.srv.URL+r.path, body)
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	ip := r.ip
	if ip == "" {
		e.mu.Lock()
		e.ip++
		ip = fmt.Sprintf("10.%d.%d.%d", e.ip/65536%256, e.ip/256%256, e.ip%256)
		e.mu.Unlock()
	}
	req.Header.Set("X-Forwarded-For", ip)
	res, err := testClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return response{res.StatusCode, res.Header, data}
}

var testClient = &http.Client{Transport: http.DefaultTransport}

func rnd(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func hashOf(b []byte) []byte { return auth.Hash(b) }

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// keys are one account's client-side values.
type keys struct {
	email, library                                          string
	salt, authKey, recoveryAuthKey, wrappedPw, wrappedRecov []byte
}

func newKeys(email string) keys {
	return keys{email: email, library: auth.NewID(), salt: rnd(16), authKey: rnd(32), recoveryAuthKey: rnd(32),
		wrappedPw: rnd(74), wrappedRecov: rnd(74)}
}

var v1KDF = map[string]any{"alg": "argon2id13", "t": 3, "m_kib": 262144, "p": 1}

func (k keys) signupBody() map[string]any {
	return map[string]any{"email": k.email, "salt": b64(k.salt), "kdf": v1KDF, "auth_key": b64(k.authKey),
		"recovery_auth_key": b64(k.recoveryAuthKey), "wrapped_master_password": b64(k.wrappedPw),
		"wrapped_master_recovery": b64(k.wrappedRecov), "library_id": k.library}
}

// account signs up and verifies; it returns the token and the verify response.
func (e *env) account(k keys) (string, map[string]any) {
	e.t.Helper()
	if r := e.do(request{method: "POST", path: "/v1/auth/signup", body: k.signupBody()}); r.status != 202 {
		e.t.Fatalf("signup %d %s", r.status, r.body)
	}
	r := e.do(request{method: "POST", path: "/v1/auth/verify", body: map[string]any{"email": k.email, "code": e.mail.LastCode(k.email), "device_name": "Mac"}})
	if r.status != 200 {
		e.t.Fatalf("verify %d %s", r.status, r.body)
	}
	m := r.json(e.t)
	return m["token"].(string), m
}
