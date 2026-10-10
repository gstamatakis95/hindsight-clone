package reflect

import (
	"fmt"
	"memd/internal/domain"
)

// addUsage merges one call's usage into st (one entry per slot and model) and enforces the whole-call token
// budget: past maxTotal (nil or <= 0 = unlimited) reflect fails with RESOURCE_EXHAUSTED.
func addUsage(st *AgentState, u domain.Usage, maxTotal *int64) error {
	if u.InputTokens != 0 || u.OutputTokens != 0 {
		merged := false
		for i := range st.Usage {
			if st.Usage[i].Slot == u.Slot && st.Usage[i].Model == u.Model {
				st.Usage[i].InputTokens += u.InputTokens
				st.Usage[i].OutputTokens += u.OutputTokens
				merged = true
				break
			}
		}
		if !merged {
			st.Usage = append(st.Usage, u)
		}
	}
	st.TotalTokens += u.InputTokens + u.OutputTokens
	if maxTotal != nil && *maxTotal > 0 && st.TotalTokens > *maxTotal {
		return fmt.Errorf("%w: reflect used %d tokens, over max_total_tokens %d",
			domain.ErrResourceExhausted, st.TotalTokens, *maxTotal)
	}
	return nil
}
