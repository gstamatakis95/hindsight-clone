package ai

import (
	"errors"
	"memd/internal/domain"
	"testing"
)

// TestResolve: a slot's own binding wins over the default, which fills only chat-model slots; an unbound slot
// or an empty model is ErrModelUnbound (so FAILED_PRECONDITION, never INVALID_ARGUMENT); EMBED and the
// unspecified slot are ErrInvalidArgument.
func TestResolve(t *testing.T) {
	temp := 0.2
	cfg := domain.BankConfig{Models: &domain.ModelBindings{
		Default: &domain.ModelBinding{Model: "def"},
		Extract: &domain.ModelBinding{Model: "ext", Temperature: &temp},
		Reflect: &domain.ModelBinding{},
	}}
	tests := []struct {
		name  string
		slot  domain.ModelSlot
		model string
		err   error
	}{
		{"own binding", domain.ModelSlotExtract, "ext", nil},
		{"default", domain.ModelSlotConsolidate, "def", nil},
		{"default never binds rerank", domain.ModelSlotRerank, "", domain.ErrModelUnbound},
		{"empty model", domain.ModelSlotReflect, "", domain.ErrModelUnbound},
		{"embed", domain.ModelSlotEmbed, "", domain.ErrInvalidArgument},
		{"unspecified", domain.ModelSlotUnspecified, "", domain.ErrInvalidArgument},
	}
	r := NewRouter()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := r.Resolve(cfg, tc.slot)
			if tc.err == nil && err != nil || tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if b.Model != tc.model {
				t.Fatalf("model = %q, want %q", b.Model, tc.model)
			}
		})
	}
	if _, err := r.Resolve(domain.BankConfig{}, domain.ModelSlotExtract); !errors.Is(err, domain.ErrModelUnbound) {
		t.Fatalf("no models: err = %v, want ErrModelUnbound", err)
	}
	if b, _ := r.Resolve(cfg, domain.ModelSlotExtract); b.Temperature == nil || *b.Temperature != temp {
		t.Fatal("binding parameters not returned")
	}
}
