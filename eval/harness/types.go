package harness

import "time"

// Judge kinds pick the grading prompt for a question. The empty kind is the default prompt.
const (
	// KindDefault grades whether the response contains the gold answer.
	KindDefault = ""
	// KindTemporal is KindDefault that also forgives off-by-one day counts.
	KindTemporal = "temporal"
	// KindUpdate accepts a response that mentions old information as long as the updated answer is given.
	KindUpdate = "update"
	// KindPreference grades a personalised response against a rubric in Question.Answer.
	KindPreference = "preference"
	// KindAbstention grades whether the response correctly declines an unanswerable question.
	KindAbstention = "abstention"
)

// Conversation is one benchmark unit: its sessions go into one fresh bank and its questions are asked
// against that bank.
type Conversation struct {
	// ID is unique within a dataset; the bank id is derived from it.
	ID string
	// Sessions are retained in order, one Retain call per session.
	Sessions []Session
	// Questions are asked after the barrier, in order.
	Questions []Question
}

// Session is one retained document: a chat session or a fixture item.
type Session struct {
	// ID becomes the document id, so re-running the same session is an idempotent upsert.
	ID string
	// Time is when the session happened; zero means unknown (the server uses now).
	Time time.Time
	// Context tells the extractor who is speaking or where the content came from.
	Context string
	// Content is the session text, one "speaker: text" line per turn for chats.
	Content string
}

// Question is one benchmark question with its gold answer.
type Question struct {
	// ID is unique within its conversation.
	ID string
	// Category is the report bucket (LongMemEval question_type, LoCoMo category name, fixture name).
	Category string
	// Query is the question text sent to Recall or Reflect.
	Query string
	// Answer is the gold answer (or rubric / explanation, depending on Kind).
	Answer string
	// Accept lists substrings the deterministic judge accepts; empty = Answer.
	Accept []string
	// Kind picks the judge prompt; see the Kind constants.
	Kind string
	// QueryTime anchors relative expressions in the query; zero = the server's now.
	QueryTime time.Time
}

// Usage is token usage for one model slot (memd ModelSlot name without its prefix, or an eval-side
// slot such as JUDGE).
type Usage struct {
	// Slot is the model slot, for example EXTRACT, REFLECT or JUDGE.
	Slot string `json:"slot"`
	// InputTokens are prompt tokens.
	InputTokens int64 `json:"input_tokens"`
	// OutputTokens are completion tokens.
	OutputTokens int64 `json:"output_tokens"`
}
