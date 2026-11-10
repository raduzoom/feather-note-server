//go:build integration

// Package e2e is the roadmap's exit criterion: a scripted client signs up, verifies, pushes, pulls, hits the
// quota and is refused, deletes the account and finds no blobs left. The server's log is captured throughout
// and must hold none of the client's secrets or content.
package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"feathernote/server/internal/api"
	"feathernote/server/internal/auth"
	"feathernote/server/internal/blobs/blobstest"
	"feathernote/server/internal/config"
	"feathernote/server/internal/janitor"
	"feathernote/server/internal/mail/mailtest"
	"feathernote/server/internal/store/storetest"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func rnd(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func TestScriptedClient(t *testing.T) {
	db := storetest.NewDB(t)
	storage, s3cfg := blobstest.NewStorage(t)
	mailer := mailtest.New()
	logs := &lockedBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	cfg := config.Config{ServerSecret: rnd(32), BillingEnabled: false, QuotaBytes: 200000, MaxBlobBytes: 104857600, Mail: "log", S3: s3cfg}
	srv := httptest.NewServer(api.NewRouter(api.Deps{Config: cfg, DB: db, Mailer: mailer, Storage: storage, Clock: time.Now, Log: log, Version: "e2e"}))
	defer srv.Close()
	ctx := context.Background()

	call := func(method, path, token string, body any) (int, []byte) {
		t.Helper()
		var r io.Reader
		switch b := body.(type) {
		case nil:
		case []byte:
			r = bytes.NewReader(b)
		default:
			data, _ := json.Marshal(b)
			r = bytes.NewReader(data)
		}
		req, _ := http.NewRequest(method, srv.URL+"/v1"+path, r)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		return res.StatusCode, data
	}
	b64 := base64.StdEncoding.EncodeToString
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

	// 1. server, signup, verify
	email := "scripted.client@example.com"
	authKey, library := rnd(32), auth.NewID()
	if st, _ := call("GET", "/server", "", nil); st != 200 {
		t.Fatal("server", st)
	}
	st, body := call("POST", "/auth/signup", "", map[string]any{"email": email, "salt": b64(rnd(16)),
		"kdf": map[string]any{"alg": "argon2id13", "t": 3, "m_kib": 262144, "p": 1}, "auth_key": b64(authKey),
		"recovery_auth_key": b64(rnd(32)), "wrapped_master_password": b64(rnd(74)), "wrapped_master_recovery": b64(rnd(74)), "library_id": library})
	if st != 202 {
		t.Fatal("signup", st, string(body))
	}
	code := mailer.LastCode(email)
	st, body = call("POST", "/auth/verify", "", map[string]any{"email": email, "code": code, "device_name": "Scripted Mac"})
	if st != 200 {
		t.Fatal("verify", st, string(body))
	}
	var v struct{ Token string }
	_ = json.Unmarshal(body, &v)

	// 2. a blob, a note referencing it, a tag; pull both back
	note := rnd(60000)
	if st, body := call("PUT", "/blobs/"+sum(note), v.Token, note); st != 200 {
		t.Fatal("blob", st, string(body))
	}
	secretTitle := []byte("TOP-SECRET-TITLE Groceries")
	meta := append(rnd(40), secretTitle...)
	noteID, tagID := auth.NewID(), auth.NewID()
	st, body = call("POST", "/sync/push", v.Token, map[string]any{"library_id": library, "changes": []map[string]any{
		{"id": noteID, "type": "note", "base_version": 0, "deleted": false, "meta": b64(meta), "blob_sha256": sum(note)},
		{"id": tagID, "type": "tag", "base_version": 0, "deleted": false, "meta": b64(rnd(30)), "blob_sha256": nil},
	}})
	if st != 200 || strings.Count(string(body), `"accepted"`) != 2 {
		t.Fatal("push", st, string(body))
	}
	st, body = call("GET", "/sync/pull?library_id="+library, v.Token, nil)
	if st != 200 || !strings.Contains(string(body), noteID) || !strings.Contains(string(body), tagID) {
		t.Fatal("pull", st, string(body))
	}

	// 3. upload until the 200,000-byte quota refuses
	refused := false
	for i := 0; i < 5 && !refused; i++ {
		data := rnd(50000)
		st, body = call("PUT", "/blobs/"+sum(data), v.Token, data)
		refused = st == 403 && strings.Contains(string(body), "quota_exceeded")
	}
	if !refused {
		t.Fatal("the quota never refused an upload")
	}

	// 4. delete the account; after the janitor, nothing is left in storage
	var accountID string
	_ = db.QueryRow(ctx, "SELECT id FROM accounts WHERE email_norm = $1", email).Scan(&accountID)
	if st, body := call("DELETE", "/account", v.Token, map[string]any{"auth_key": b64(authKey)}); st != 202 {
		t.Fatal("delete", st, string(body))
	}
	if err := (&janitor.Janitor{DB: db, Storage: storage, Clock: time.Now, Log: log}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if keys, err := storage.List(ctx, accountID+"/"); err != nil || len(keys) != 0 {
		t.Fatalf("blobs left: %v %v", keys, err)
	}

	// 5. the log never held the client's secrets or content
	out := logs.String()
	for name, secret := range map[string]string{"email": email, "code": code, "token": v.Token, "auth key": b64(authKey),
		"meta": b64(meta), "title": string(secretTitle), "library": library} {
		if strings.Contains(out, secret) {
			t.Errorf("the log contains the %s", name)
		}
	}
	if !strings.Contains(out, `"path":"/v1/sync/push"`) {
		t.Fatal("the log captured nothing")
	}
}
