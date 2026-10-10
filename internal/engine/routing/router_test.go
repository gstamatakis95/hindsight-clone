package routing

import (
	"errors"
	"memd/internal/domain"
	"testing"
)

func TestResolve(t *testing.T) {
	temp := 0.2
	cfg := domain.BankConfig{Models: []domain.SlotBinding{
		{Slot: domain.ModelSlotExtract, Binding: domain.ModelBinding{Model: "ext", Temperature: &temp}},
		{Slot: domain.ModelSlotRerank, Binding: domain.ModelBinding{Model: "rr"}},
		{Slot: domain.ModelSlotReflect, Binding: domain.ModelBinding{}},
	}}
	tests := []struct {
		name  string
		slot  domain.ModelSlot
		model string
		err   error
	}{
		{"bound", domain.ModelSlotExtract, "ext", nil},
		{"other slot bound", domain.ModelSlotRerank, "rr", nil},
		{"unbound", domain.ModelSlotConsolidate, "", domain.ErrInvalidArgument},
		{"empty model", domain.ModelSlotReflect, "", domain.ErrInvalidArgument},
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
	if b, _ := r.Resolve(cfg, domain.ModelSlotExtract); b.Temperature == nil || *b.Temperature != temp {
		t.Fatal("binding parameters not returned")
	}
}
