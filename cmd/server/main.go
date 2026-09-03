// Command server wires Open -> Store -> DrawService -> Hub and starts :8080.
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"cd-debate-tab/internal/database"
	"cd-debate-tab/internal/draw"
	"cd-debate-tab/internal/handlers"
	"cd-debate-tab/internal/httpx"
	"cd-debate-tab/internal/store"
	"cd-debate-tab/internal/stream"
)

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
	svc := draw.CryptoDefaults(st)
	hub := stream.New()
	tmpl, err := handlers.LoadTemplates()
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for range t.C {
			_ = db.Ping()
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
	log.Fatal(http.ListenAndServe(":8080", root))
}
