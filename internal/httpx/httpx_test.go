package httpx

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestErrorBody(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, ErrQuotaExceeded)
	if rec.Code != 403 || strings.TrimSpace(rec.Body.String()) != `{"error":{"code":"quota_exceeded","message":"Storage quota exceeded."}}` {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	WriteError(rec, errors.New("boom"))
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), `"server_error"`) || strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type %q", ct)
	}
}

func TestDecode(t *testing.T) {
	var v struct {
		A int `json:"a"`
	}
	ok := httptest.NewRequest("POST", "/", strings.NewReader(`{"a":1,"later":"field"}`))
	if err := Decode(ok, &v, 100); err != nil || v.A != 1 {
		t.Fatalf("%v %v", err, v)
	}
	for _, body := range []string{"", "{", `{"a":"x"}`, `{"a":1}` + strings.Repeat(" ", 200)} {
		err := Decode(httptest.NewRequest("POST", "/", strings.NewReader(body)), &v, 100)
		var e *Error
		if !errors.As(err, &e) || e.Code != "invalid_request" || e.Status != 400 {
			t.Errorf("body %q: %v", body, err)
		}
	}
}

func TestB64AndTime(t *testing.T) {
	b, _ := B64([]byte{0xfb, 0xff}).MarshalJSON()
	if string(b) != `"+/8="` {
		t.Fatalf("%s", b)
	}
	var back B64
	if err := back.UnmarshalJSON([]byte(`"+/8="`)); err != nil || string(back) != "\xfb\xff" {
		t.Fatalf("%v %x", err, back)
	}
	if back.UnmarshalJSON([]byte(`"-_8"`)) == nil {
		t.Fatal("base64url accepted")
	}
}
