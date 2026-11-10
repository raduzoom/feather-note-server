package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Review I2: a request body that trickles in is cut off; blob uploads get longer.
func TestSlowBodiesAreCutOff(t *testing.T) {
	h := readDeadline(50*time.Millisecond, time.Hour, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(408)
			return
		}
		w.WriteHeader(200)
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("{"))
		time.Sleep(300 * time.Millisecond)
		_, _ = pw.Write([]byte("}"))
		pw.Close()
	}()
	res, err := http.Post(srv.URL+"/v1/sync/push", "application/json", pr)
	if err == nil && res.StatusCode == 200 {
		t.Fatal("a body slower than the deadline was read in full")
	}
	fast, err := http.Post(srv.URL+"/v1/blobs/x", "application/octet-stream", strings.NewReader("ok"))
	if err != nil || fast.StatusCode != 200 {
		t.Fatalf("%v %v", err, fast)
	}
}
