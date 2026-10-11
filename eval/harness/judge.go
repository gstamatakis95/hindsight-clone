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
	// NotApplicable is true when the question can't be graded in this mode (for example an abstention
	// question in recall mode); it counts neither as correct nor as wrong.
	NotApplicable bool
	// Reason is the judge's short explanation, or its raw reply.
	Reason string
	// Usage is the judge's own token usage, reported under the JUDGE slot.
	Usage []Usage
}

// Answerer writes the answer to a question from recalled memories (rag mode), the way a client
// application puts recall results in front of its own model.
type Answerer interface {
	// Answer answers q from the memories in recalled.
	Answer(ctx context.Context, q Question, recalled Recalled) (Answer, error)
}

// Answer is an Answerer's reply.
type Answer struct {
	// Text is the final answer.
	Text string
	// Reasoning is the model's explanation, when it gave one.
	Reasoning string
	// Usage is the answer model's token usage, reported under the ANSWER slot.
	Usage []Usage
}
