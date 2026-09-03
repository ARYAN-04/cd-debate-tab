package handlers

import (
	"html/template"
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

// Index renders the public draw page with SSE listener.
func (p Public) Index(w http.ResponseWriter, r *http.Request) {
	allocs, err := p.Store.SearchAllocations(r.Context(), "")
	if err != nil {
		httpErr(w, 500, "search failed")
		return
	}
	render(w, p.Tmpl, "draw", map[string]any{"Allocs": allocs})
}

// Search returns the capped LIKE-filtered grid partial.
func (p Public) Search(w http.ResponseWriter, r *http.Request) {
	allocs, err := p.Store.SearchAllocations(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		httpErr(w, 500, "search failed")
		return
	}
	render(w, p.Tmpl, "draw_grid", map[string]any{"Allocs": allocs})
}

// loginLimiter caps POST /login attempts per IP (20/min) to slow brute force.
type loginLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

// allow reports whether ip may attempt login now, recording the attempt.
func (l *loginLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	kept := make([]time.Time, 0, len(l.hits[ip]))
	for _, t := range l.hits[ip] {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	if len(kept) >= 20 {
		l.hits[ip] = kept
		return false
	}
	l.hits[ip] = append(kept, now)
	return true
}

// RegisterAuth wires login/logout; logout requires auth middleware.
func RegisterAuth(mux *http.ServeMux, s *store.Store, t *template.Template) {
	_ = t
	limiter := &loginLimiter{hits: map[string][]time.Time{}}
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		msg := ""
		if r.URL.Query().Get("err") != "" {
			msg = `<p style="color:red">Invalid username or password.</p>`
		}
		_, _ = w.Write([]byte(msg + `<form method="post" action="/login"><input name="username" autocomplete="username"><input name="password" type="password" autocomplete="current-password"><button>Login</button></form>`))
	})
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		if !limiter.allow(r.RemoteAddr) {
			httpErr(w, http.StatusTooManyRequests, "too many attempts")
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/login?err=1", http.StatusSeeOther)
			return
		}
		username := strings.TrimSpace(r.FormValue("username"))
		admin, err := s.GetAdminByUsername(r.Context(), username)
		if err != nil || auth.CheckPassword(admin.PasswordHash, r.FormValue("password")) != nil {
			http.Redirect(w, r, "/login?err=1", http.StatusSeeOther)
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
		auth.SetSessionCookie(w, token, r.TLS != nil, exp)
		http.Redirect(w, r, "/admin/teams", http.StatusSeeOther)
	})
	mux.Handle("POST /logout", auth.RequireAuth(s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(auth.SessionCookie); err == nil {
			_ = s.DeleteSession(r.Context(), c.Value)
		}
		auth.ClearSessionCookie(w)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})))
}
