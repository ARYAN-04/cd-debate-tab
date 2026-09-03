package handlers

import (
	"html/template"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"cd-debate-tab/internal/auth"
	"cd-debate-tab/internal/store"
)

// Public routes the draw view and HTMX search.
type Public struct {
	Store *store.Store
	Tmpl  *template.Template
}

// NewPublic wires deps for the public routes.
func NewPublic(s *store.Store, t *template.Template) Public {
	return Public{Store: s, Tmpl: t}
}

// RegisterPublic wires public routes onto mux.
func RegisterPublic(mux *http.ServeMux, p Public) {
	mux.HandleFunc("GET /", p.Index)
	mux.HandleFunc("GET /draw/search", p.Search)
}

// Index renders the public draw page with SSE listener, scoped to one
// round: ?round=ID, else the highest-order published round.
func (p Public) Index(w http.ResponseWriter, r *http.Request) {
	rounds, err := p.Store.ListPublicRounds(r.Context())
	if err != nil {
		httpErr(w, 500, "list failed")
		return
	}
	roundID := p.resolveRound(r.URL.Query().Get("round"), rounds)
	allocs, err := p.Store.SearchPublicAllocations(r.Context(), "", roundID)
	if err != nil {
		httpErr(w, 500, "search failed")
		return
	}
	render(w, p.Tmpl, "draw", map[string]any{
		"Rooms": GroupDraw(allocs), "IsAdmin": p.isAdmin(r),
		"Rounds": rounds, "CurrentRound": roundID,
	})
}

// resolveRound keeps an explicitly requested round, else the current one
// (first of ListPublicRounds: highest order), else empty.
func (p Public) resolveRound(want string, rounds []store.Round) string {
	for _, rd := range rounds {
		if rd.ID == want {
			return want
		}
	}
	if len(rounds) > 0 {
		return rounds[0].ID
	}
	return ""
}

// isAdmin reports whether the request carries a live session cookie.
func (p Public) isAdmin(r *http.Request) bool {
	c, err := r.Cookie(auth.SessionCookie)
	if err != nil || c.Value == "" {
		return false
	}
	sess, err := p.Store.GetSessionByToken(r.Context(), c.Value)
	if err != nil {
		return false
	}
	return !auth.Expired(sess.ExpiresAt, time.Now())
}

// Search returns the capped LIKE-filtered grid partial (same round scope).
func (p Public) Search(w http.ResponseWriter, r *http.Request) {
	rounds, err := p.Store.ListPublicRounds(r.Context())
	if err != nil {
		httpErr(w, 500, "list failed")
		return
	}
	roundID := p.resolveRound(r.URL.Query().Get("round"), rounds)
	allocs, err := p.Store.SearchPublicAllocations(r.Context(), r.URL.Query().Get("q"), roundID)
	if err != nil {
		httpErr(w, 500, "search failed")
		return
	}
	render(w, p.Tmpl, "draw_grid", map[string]any{"Rooms": GroupDraw(allocs)})
}

// dummyHash provides constant-time bcrypt verification when a user is not found.
var dummyHash, _ = auth.HashPassword("invalid-password-never-matches")

// loginLimiter caps POST /login attempts per IP (20/min) to slow brute force.
type loginLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

// allow reports whether ip may attempt login now, recording the attempt.
func (l *loginLimiter) allow(remoteAddr string) bool {
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		ip = remoteAddr
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	// Prune old entries to prevent unbounded memory growth.
	for k, times := range l.hits {
		var valid []time.Time
		for _, t := range times {
			if t.After(cutoff) {
				valid = append(valid, t)
			}
		}
		if len(valid) == 0 {
			delete(l.hits, k)
		} else {
			l.hits[k] = valid
		}
	}
	kept := l.hits[ip]
	if len(kept) >= 20 {
		return false
	}
	l.hits[ip] = append(kept, now)
	return true
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// RegisterAuth wires login/logout; logout requires auth middleware.
func RegisterAuth(mux *http.ServeMux, s *store.Store, t *template.Template) {
	limiter := &loginLimiter{hits: map[string][]time.Time{}}
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		render(w, t, "login", map[string]any{"Err": r.URL.Query().Get("err") != ""})
	})
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		fail := func() { http.Redirect(w, r, "/login?err=1", http.StatusSeeOther) }
		if !limiter.allow(r.RemoteAddr) {
			httpErr(w, http.StatusTooManyRequests, "too many attempts")
			return
		}
		if err := r.ParseForm(); err != nil {
			fail()
			return
		}
		username := strings.TrimSpace(r.FormValue("username"))
		admin, err := s.GetAdminByUsername(r.Context(), username)
		hash := dummyHash
		if err == nil {
			hash = admin.PasswordHash
		}
		pwErr := auth.CheckPassword(hash, r.FormValue("password"))
		if err != nil || pwErr != nil {
			fail()
			return
		}
		token, err := auth.GenerateToken()
		if err != nil {
			httpErr(w, 500, "session failed")
			return
		}
		exp := auth.Expiry(time.Now())
		if err := s.CreateSession(r.Context(), token, admin.ID, exp); err != nil {
			httpErr(w, 500, "session failed")
			return
		}
		auth.SetSessionCookie(w, token, isHTTPS(r), exp)
		http.Redirect(w, r, "/admin/teams", http.StatusSeeOther)
	})
	mux.Handle("POST /logout", auth.RequireAuth(s, auth.CSRFProtect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(auth.SessionCookie); err == nil {
			_ = s.DeleteSession(r.Context(), c.Value)
		}
		auth.ClearSessionCookie(w)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}))))
}
