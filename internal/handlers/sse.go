package handlers

import (
	"fmt"
	"net/http"

	"cd-debate-tab/internal/stream"
)

// SSE is the streaming handler only (Hub lives in stream/).
type SSE struct {
	Hub *stream.Hub
}

// NewSSE injects the Hub for the streaming handler.
func NewSSE(h *stream.Hub) SSE {
	return SSE{Hub: h}
}

// RegisterSSE wires /events onto mux.
func RegisterSSE(mux *http.ServeMux, s SSE) {
	mux.HandleFunc("GET /events", s.Events)
}

// Events streams newline-stripped frames; reaps on r.Context().Done() or channel close.
func (s SSE) Events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ch := s.Hub.Subscribe()
	if ch == nil {
		httpErr(w, http.StatusServiceUnavailable, "too many connections")
		return
	}
	defer s.Hub.Unsubscribe(ch)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	_, _ = fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			_, _ = fmt.Fprint(w, msg)
			flusher.Flush()
		}
	}
}
