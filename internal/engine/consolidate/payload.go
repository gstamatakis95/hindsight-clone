package consolidate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
)

// SerialKey is the consolidate job's serial key: one consolidation at a time per bank.
const SerialKey = "consolidate"

// Payload is the consolidate job's payload. The automatic job (retain, rederive, retag, clears, the
// maintenance tick) carries the empty object {}: every scope, bank defaults.
type Payload struct {
	// Scopes limits a manual run to units whose tags contain one of these tag sets; empty = every unit.
	Scopes []domain.TagSet `json:"scopes,omitempty"`
}

// NewJob returns a consolidate job for b. A job with scopes or overrides is never deduplicated; the
// automatic one (no scopes, no overrides) joins a pending consolidate of the bank.
func NewJob(b port.BankRef, scopes []domain.TagSet, overrides *domain.BankConfig) port.Job {
	payload := []byte(`{}`)
	if len(scopes) > 0 {
		// Marshalling a struct of string slices cannot fail.
		if b, err := json.Marshal(Payload{Scopes: scopes}); err == nil {
			payload = b
		}
	}
	dedupe := port.DedupePending
	if len(scopes) > 0 || overrides != nil {
		dedupe = port.DedupeNone
	}
	return port.Job{
		Bank:      b,
		Kind:      domain.OperationKindConsolidate,
		SerialKey: SerialKey,
		Payload:   payload,
		Overrides: overrides,
		Dedupe:    dedupe,
	}
}

// decodePayload reads a consolidate payload; an empty or null payload is the automatic job.
func decodePayload(raw json.RawMessage) (Payload, error) {
	var p Payload
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return p, nil
	}
	if err := json.Unmarshal(trimmed, &p); err != nil {
		return Payload{}, fmt.Errorf("%w: consolidate payload: %w", domain.ErrInvalidArgument, err)
	}
	return p, nil
}

// payloadScopes turns the payload's tag sets into scope keys of b.
func payloadScopes(b port.BankRef, p Payload) []port.ScopeKey {
	out := make([]port.ScopeKey, 0, len(p.Scopes))
	for _, s := range p.Scopes {
		out = append(out, domain.NewScopeKey(b, s.Tags))
	}
	return out
}
