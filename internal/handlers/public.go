package handlers

import (
	"html/template"
	"net/http"

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

// RegisterAuth wires login/logout; logout requires auth middleware.
func RegisterAuth(mux *http.ServeMux, s *store.Store, t *template.Template) {
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<form method="post" action="/login"><input name="username"><input name="password" type="password"><button>Login</button></form>`))
	})
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		_ = s
		_ = t
		http.Redirect(w, r, "/admin/teams", http.StatusSeeOther)
	})
	mux.Handle("POST /logout", auth.RequireAuth(s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "", MaxAge: -1, Path: "/"})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})))
}
