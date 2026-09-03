package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"time"
)

type ctxKey string

// requestIDKey carries the request ID through the request context.
const requestIDKey ctxKey = "requestID"

// RequestID annotates each request with a random ID, exposed via the
// X-Request-ID response header and the request context.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			next.ServeHTTP(w, r)
			return
		}
		id := hex.EncodeToString(b[:])
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFrom returns the request ID stored by RequestID.
func RequestIDFrom(r *http.Request) (string, bool) {
	id, ok := r.Context().Value(requestIDKey).(string)
	return id, ok
}

// Recover guards against panics, converting them to 500s.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v method=%s path=%s", rec, r.Method, r.URL.Path)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusWriter captures the response status for Logging.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Logging logs method, path, status, and duration with stdlib log only.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		id, _ := RequestIDFrom(r)
		log.Printf("method=%s path=%s status=%d duration=%s request_id=%s",
			r.Method, r.URL.Path, sw.status, time.Since(start), id)
	})
}
