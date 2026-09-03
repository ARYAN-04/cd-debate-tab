package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestRequestIDHeader(t *testing.T) {
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = RequestIDFrom(r)
	})
	rec := httptest.NewRecorder()
	RequestID(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing X-Request-ID header")
	}
	if seen == "" {
		t.Fatal("request ID missing from context")
	}
}

func TestRecoverPanics(t *testing.T) {
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	rec := httptest.NewRecorder()
	Recover(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rec.Code)
	}
}

func TestParseFSFuncMap(t *testing.T) {
	fsys := fstest.MapFS{
		"hello.html": {Data: []byte(`{{inc 1}}{{add 1 2}}{{upper "ab"}}`)},
	}
	tmpl, err := ParseFS(fsys, "hello.html")
	if err != nil {
		t.Fatal(err)
	}
	_ = tmpl
	var sb strings.Builder
	if err := tmpl.ExecuteTemplate(&sb, "hello.html", nil); err != nil {
		t.Fatal(err)
	}
	if sb.String() != "23AB" {
		t.Fatalf("got %q, want %q", sb.String(), "23AB")
	}
}
