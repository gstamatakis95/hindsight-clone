package judge

import (
	"context"
	"fmt"
	"memd/eval/harness"
	"strings"
)

// Contains is a deterministic judge for golden fixtures: a response is correct when it contains, case
// insensitively, any of the question's Accept substrings (or its Answer when Accept is empty). It makes
// no model calls, so CI can run fixtures against the fake gateway.
type Contains struct{}

var _ harness.Judge = Contains{}

// Grade checks response for the accepted substrings.
func (Contains) Grade(_ context.Context, q harness.Question, response string, _ bool) (harness.Verdict, error) {
	accept := q.Accept
	if len(accept) == 0 {
		accept = []string{q.Answer}
	}
	lower := strings.ToLower(response)
	for _, a := range accept {
		if a != "" && strings.Contains(lower, strings.ToLower(a)) {
			return harness.Verdict{Correct: true, Reason: fmt.Sprintf("found %q", a)}, nil
		}
	}
	return harness.Verdict{Reason: fmt.Sprintf("none of %q found", accept)}, nil
}
