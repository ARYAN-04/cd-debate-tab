package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBcryptRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckPassword(hash, "correct-horse"); err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
	if err := CheckPassword(hash, "wrong"); err == nil {
		t.Fatal("invalid password accepted")
	}
}

func TestGenerateTokenUnique(t *testing.T) {
	a, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 64 || len(b) != 64 {
		t.Fatalf("expected 64-char hex, got %q %q", a, b)
	}
	if a == b {
		t.Fatal("tokens must differ")
	}
}

func TestExpiryHelper(t *testing.T) {
	now := time.Now()
	if !Expired(now.Add(-time.Second), now) {
		t.Fatal("past expiry must read expired")
	}
	if Expired(Expiry(now), now) {
		t.Fatal("fresh expiry must not read expired")
	}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestCSRFGetSetsCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	CSRFProtect(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET code = %d", rec.Code)
	}
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == CSRFCookie && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("GET must mint csrf cookie")
	}
}

func TestCSRFRejectAccept(t *testing.T) {
	// Reject: POST with no token at all.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	CSRFProtect(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bare POST code = %d, want 403", rec.Code)
	}

	// Reject: HTMX POST with no token (no HX-Request exemption).
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("HX-Request", "true")
	CSRFProtect(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("htmx POST code = %d, want 403", rec.Code)
	}

	// Accept: matching header + cookie.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("x=1"))
	req.AddCookie(&http.Cookie{Name: CSRFCookie, Value: "tok123"})
	req.Header.Set(CSRFHeader, "tok123")
	req.Header.Set("HX-Request", "true")
	CSRFProtect(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid POST code = %d, want 200", rec.Code)
	}

	// Reject: header/cookie mismatch.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookie, Value: "tok123"})
	req.Header.Set(CSRFHeader, "other")
	CSRFProtect(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("mismatch POST code = %d, want 403", rec.Code)
	}
}
