package stream

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestEncodeSSEFrameStripsNewlines(t *testing.T) {
	got := EncodeSSEFrame("draw-published", "line1\nline2\r\nline3\rtail")
	if !strings.HasPrefix(got, "event: draw-published\n") {
		t.Fatalf("missing event line: %q", got)
	}
	if !strings.Contains(got, "retry: 3000\n\n") {
		t.Fatalf("missing retry trailer: %q", got)
	}
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		if strings.HasPrefix(line, "data:") && strings.ContainsAny(line[len("data:"):], "\n\r") {
			t.Fatalf("raw newline in data line: %q", got)
		}
	}
	if strings.Contains(got, "line1line2") || !strings.Contains(got, "line1 line2 line3 tail") {
		t.Fatalf("newlines must become spaces: %q", got)
	}
}

func TestBroadcastDropsSlowClient(t *testing.T) {
	h := New()
	slow := h.Register()
	fast := h.Register()

	// Fill slow client's buffer (cap 4) without reading.
	for i := 0; i < 4; i++ {
		slow <- "prefill"
	}
	h.Broadcast("draw")

	if h.Count() != 1 {
		t.Fatalf("slow client must be dropped, count = %d", h.Count())
	}
	select {
	case msg := <-fast:
		if msg != "draw" {
			t.Fatalf("fast client got %q", msg)
		}
	default:
		t.Fatal("fast client must receive broadcast")
	}
	for {
		_, ok := <-slow
		if !ok {
			break // dropped client channel is closed
		}
	}
}

func TestRunReapsOnCancel(t *testing.T) {
	h := New()
	h.Register()
	h.Register()
	if h.Count() != 2 {
		t.Fatalf("count = %d, want 2", h.Count())
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if h.Count() != 0 {
		t.Fatalf("clients must be reaped, count = %d", h.Count())
	}
}
