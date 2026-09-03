// Command server wires Open -> Store -> DrawService -> Hub and starts :8080.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"cd-debate-tab/internal/auth"
	"cd-debate-tab/internal/database"
	"cd-debate-tab/internal/draw"
	"cd-debate-tab/internal/handlers"
	"cd-debate-tab/internal/httpx"
	"cd-debate-tab/internal/store"
	"cd-debate-tab/internal/stream"
)

// seedAdmin ensures a first admin exists from ADMIN_USER/ADMIN_PASS,
// defaulting to admin/admin for local dev with a loud warning.
func seedAdmin(st *store.Store) {
	user, pass := os.Getenv("ADMIN_USER"), os.Getenv("ADMIN_PASS")
	if user == "" || pass == "" {
		user, pass = "admin", "admin"
		log.Print("WARNING: ADMIN_USER/ADMIN_PASS not set; using admin/admin locally only")
	}
	hash, err := auth.HashPassword(pass)
	if err != nil {
		log.Fatal(err)
	}
	if err := st.EnsureAdmin(context.Background(), user, hash); err != nil {
		log.Fatal(err)
	}
}

func main() {
	db, err := database.Open("debate.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	st := store.New(db)
	if err := st.InitSchema(context.Background()); err != nil {
		log.Fatal(err)
	}
	seedAdmin(st)
	svc := draw.CryptoDefaults(st)
	hub := stream.New()
	tmpl, err := handlers.LoadTemplates()
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		t := time.NewTicker(30 * time.Minute)
		defer t.Stop()
		for range t.C {
			_, _ = st.DeleteExpiredSessions(context.Background())
		}
	}()
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	handlers.RegisterPublic(mux, handlers.NewPublic(st, tmpl))
	handlers.RegisterAuth(mux, st, tmpl)
	handlers.RegisterAdminRounds(mux, handlers.NewAdminRounds(st, svc, tmpl, hub))
	handlers.RegisterAdminTeams(mux, handlers.NewAdminTeams(st, tmpl))
	// /events bypasses Logging: statusWriter hides http.Flusher.
	events := http.NewServeMux()
	handlers.RegisterSSE(events, handlers.NewSSE(hub))
	h := httpx.RequestID(httpx.Recover(httpx.Logging(mux)))
	root := http.NewServeMux()
	root.Handle("/events", httpx.RequestID(httpx.Recover(events)))
	root.Handle("/", h)
	srv := &http.Server{
		Addr:              ":8080",
		Handler:           root,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
