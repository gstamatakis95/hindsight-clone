package gateway_test

import (
	"errors"
	"memd/internal/adapter/gateway"
	"memd/internal/domain"
	"memd/internal/port"
	"net/http"
	"testing"
)

// TestContextOverflowClassified checks CCR WP-11-1: a non-retryable 400 or 413 whose body carries an
// overflow marker is both ErrContextOverflow and ErrUnavailable; any other 4xx is only ErrUnavailable.
func TestContextOverflowClassified(t *testing.T) {
	cases := []struct {
		name     string
		code     int
		body     any
		overflow bool
	}{
		{"openai code", http.StatusBadRequest, map[string]any{"error": map[string]any{
			"code": "context_length_exceeded", "message": "too long",
		}}, true},
		{"max context", http.StatusBadRequest, map[string]any{"error": "This model's Maximum Context Length is 8192"}, true},
		{"anthropic", http.StatusBadRequest, map[string]any{"error": "prompt is too long: 210000 tokens"}, true},
		{"413 input", http.StatusRequestEntityTooLarge, map[string]any{"error": "input is too long"}, true},
		{"other 400", http.StatusBadRequest, map[string]any{"error": "bad request"}, false},
		{"marker on 404", http.StatusNotFound, map[string]any{"error": "prompt is too long"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newFakeGateway(t)
			g.chat = func(int, map[string]any) (int, any) { return c.code, c.body }
			b := binding("m")
			b.MaxRetries = ptr(2)
			req := port.ChatRequest{Slot: domain.ModelSlotReflect, Binding: b}
			_, err := g.client(gateway.Config{}).Chat(t.Context(), req)
			if !errors.Is(err, domain.ErrUnavailable) {
				t.Fatalf("err = %v, want ErrUnavailable", err)
			}
			if got := errors.Is(err, domain.ErrContextOverflow); got != c.overflow {
				t.Errorf("errors.Is(err, ErrContextOverflow) = %v, want %v (err %v)", got, c.overflow, err)
			}
			if n := len(g.bodies("chat")); n != 1 {
				t.Errorf("got %d attempts, want 1 (not retried)", n)
			}
		})
	}
}
