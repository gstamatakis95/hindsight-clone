package harness

import "context"

// Judge grades a response to a question against its gold answer.
type Judge interface {
	// Grade returns the verdict on response. Recall-mode responses are the recalled memories joined by
	// newlines, and recall is true; the judge then asks whether they contain the answer.
	Grade(ctx context.Context, q Question, response string, recall bool) (Verdict, error)
}

// Verdict is a judge's decision on one response.
type Verdict struct {
	// Correct is true when the response satisfies the gold answer.
	Correct bool
	// Reason is the judge's short explanation, or its raw reply.
	Reason string
	// Usage is the judge's own token usage, reported under the JUDGE slot.
	Usage []Usage
}
