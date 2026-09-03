package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"cd-debate-tab/internal/auth"
	"cd-debate-tab/internal/draw"
	"cd-debate-tab/internal/store"
	"cd-debate-tab/internal/stream"
)

func testStoreAndTmpl(t *testing.T) (*store.Store, *AdminTeams, *AdminRounds, *Public) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := store.New(db)
	if err := st.InitSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	hub := stream.New()
	svc := draw.CryptoDefaults(st)
	teams := NewAdminTeams(st, tmpl)
	rounds := NewAdminRounds(st, svc, tmpl, hub)
	pub := NewPublic(st, tmpl)
	return st, &teams, &rounds, &pub
}

func TestLoginRateLimiter(t *testing.T) {
	lim := &loginLimiter{hits: map[string][]time.Time{}}
	for i := 0; i < 20; i++ {
		if !lim.allow(fmt.Sprintf("192.168.1.100:%d", 50000+i)) {
			t.Fatalf("attempt %d should be allowed across different ports for same IP", i+1)
		}
	}
	// 21st attempt from same IP with a new port must be blocked
	if lim.allow("192.168.1.100:60000") {
		t.Fatal("21st attempt from same IP should be blocked by rate limiter")
	}
	// Different IP must still be allowed
	if !lim.allow("192.168.1.101:54321") {
		t.Fatal("different IP should be allowed")
	}
}

func TestSpeakerRedactHXPrompt(t *testing.T) {
	st, teams, _, _ := testStoreAndTmpl(t)
	ctx := context.Background()
	tw, err := st.CreateTeam(ctx, "Pioneer", "Speaker A", "Speaker B")
	if err != nil {
		t.Fatal(err)
	}
	speakerID := tw.Speakers[0].ID

	mux := http.NewServeMux()
	RegisterAdminTeams(mux, *teams)

	// Create valid admin session
	admPass, _ := auth.HashPassword("secret")
	if err := st.EnsureAdmin(ctx, "adm", admPass); err != nil {
		t.Fatal(err)
	}
	adm, _ := st.GetAdminByUsername(ctx, "adm")
	tok, _ := auth.GenerateToken()
	_ = st.CreateSession(ctx, tok, adm.ID, time.Now().Add(time.Hour))

	// CSRF cookie and token
	csrfTok, _ := auth.GenerateToken()

	req := httptest.NewRequest(http.MethodPatch, "/admin/speakers/"+speakerID+"/redact", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: tok})
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookie, Value: csrfTok})
	req.Header.Set(auth.CSRFHeader, csrfTok)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Prompt", "Renamed Speaker")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("redact status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "Renamed Speaker" {
		t.Fatalf("redact body = %q, want 'Renamed Speaker'", rec.Body.String())
	}

	// Verify database
	loaded, err := st.ListTeamsWithSpeakers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sp := range loaded[0].Speakers {
		if sp.ID == speakerID && sp.Name == "Renamed Speaker" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("speaker was not renamed in the database")
	}
}

func TestAdminRoundsCreateValidation(t *testing.T) {
	st, _, rounds, _ := testStoreAndTmpl(t)
	ctx := context.Background()

	mux := http.NewServeMux()
	RegisterAdminRounds(mux, *rounds)

	admPass, _ := auth.HashPassword("secret")
	_ = st.EnsureAdmin(ctx, "adm", admPass)
	adm, _ := st.GetAdminByUsername(ctx, "adm")
	tok, _ := auth.GenerateToken()
	_ = st.CreateSession(ctx, tok, adm.ID, time.Now().Add(time.Hour))
	csrfTok, _ := auth.GenerateToken()

	badInputs := []url.Values{
		{"name": {"Round 1"}, "round_order": {"invalid"}, "num_rooms": {"2"}},
		{"name": {"Round 1"}, "round_order": {"1"}, "num_rooms": {"0"}},
		{"name": {"Round 1"}, "round_order": {"-1"}, "num_rooms": {"2"}},
		{"name": {""}, "round_order": {"1"}, "num_rooms": {"2"}},
	}

	for _, vals := range badInputs {
		req := httptest.NewRequest(http.MethodPost, "/admin/rounds", strings.NewReader(vals.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: tok})
		req.AddCookie(&http.Cookie{Name: auth.CSRFCookie, Value: csrfTok})
		req.Header.Set(auth.CSRFHeader, csrfTok)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bad input %v returned code %d, want 400", vals, rec.Code)
		}
	}
}

func TestCSRFProtectionRejectsWithoutToken(t *testing.T) {
	st, _, rounds, _ := testStoreAndTmpl(t)
	ctx := context.Background()
	mux := http.NewServeMux()
	RegisterAdminRounds(mux, *rounds)

	admPass, _ := auth.HashPassword("secret")
	_ = st.EnsureAdmin(ctx, "adm", admPass)
	adm, _ := st.GetAdminByUsername(ctx, "adm")
	tok, _ := auth.GenerateToken()
	_ = st.CreateSession(ctx, tok, adm.ID, time.Now().Add(time.Hour))

	// POST without CSRF cookie or header must be rejected with 403
	vals := url.Values{"name": {"Round 1"}, "round_order": {"1"}, "num_rooms": {"2"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/rounds", strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: tok})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated CSRF request code = %d, want 403", rec.Code)
	}
}

func TestSSEEventsCleanTerminationOnClose(t *testing.T) {
	hub := stream.New()
	sseHandler := NewSSE(hub)

	rec := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		sseHandler.Events(rec, req)
		close(done)
	}()

	// Give the goroutine a moment to subscribe and flush initial frame
	time.Sleep(20 * time.Millisecond)

	// Close all client channels by running hub broadcast that drops slow clients
	hub.Broadcast("test-msg")

	// Now cancel request context to ensure clean exit
	cancel()

	select {
	case <-done:
		// Exited cleanly without infinite spinning loop
	case <-time.After(500 * time.Millisecond):
		t.Fatal("SSE Events did not terminate cleanly after context cancel")
	}
}
