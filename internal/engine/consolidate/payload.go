package consolidate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
)

// decodePayload reads a consolidate payload; an empty or null payload is the automatic job.
func decodePayload(raw json.RawMessage) (jobs.ConsolidatePayload, error) {
	var p jobs.ConsolidatePayload
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return p, nil
	}
	if err := json.Unmarshal(trimmed, &p); err != nil {
		return jobs.ConsolidatePayload{}, fmt.Errorf("%w: consolidate payload: %w", domain.ErrInvalidArgument, err)
	}
	return p, nil
}

// payloadScopes turns the payload's tag sets into scope keys of b.
func payloadScopes(b domain.BankRef, p jobs.ConsolidatePayload) []domain.ScopeKey {
	out := make([]domain.ScopeKey, 0, len(p.Scopes))
	for _, s := range p.Scopes {
		out = append(out, domain.NewScopeKey(b, s.Tags))
	}
	return out
}
