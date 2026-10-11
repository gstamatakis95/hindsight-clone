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
	// KindLoCoMo grades with the LoCoMo judge prompt of Hindsight's published runs (lenient on wording and
	// date formats) and, in rag mode, answers with the matching LoCoMo answer prompt.
	KindLoCoMo = "locomo"
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
	// Content is the session text.
	Content string
}

// Question is one benchmark question with its gold answer.
type Question struct {
	// ID is unique within its conversation.
	ID string
	// Category is the report bucket (LongMemEval question_type, LoCoMo category, fixture name).
	Category string
	// Query is the question text sent to Recall or Reflect.
	Query string
	// Answer is the gold answer (or rubric / explanation, depending on Kind).
	Answer string
	// Accept lists substrings the deterministic judge accepts; empty = Answer.
	Accept []string
	// Kind picks the judge prompt (and the rag answer prompt); see the Kind constants.
	Kind string
	// QueryTime anchors relative expressions in the query; zero = the server's now.
	QueryTime time.Time
}

// Usage is token usage for one model slot (memd ModelSlot name without its prefix, or an eval-side
// slot such as ANSWER or JUDGE).
type Usage struct {
	// Slot is the model slot, for example EXTRACT, REFLECT or JUDGE.
	Slot string `json:"slot"`
	// InputTokens are prompt tokens.
	InputTokens int64 `json:"input_tokens"`
	// OutputTokens are completion tokens.
	OutputTokens int64 `json:"output_tokens"`
}

// RecallOptions tunes a Recall call. Zero fields leave the target's defaults.
type RecallOptions struct {
	// Budget is the candidate budget: low, mid or high; empty = the target's default.
	Budget string `yaml:"budget" json:"budget,omitempty"`
	// MaxTokens bounds the packed results; 0 = the target's default.
	MaxTokens int `yaml:"max_tokens" json:"max_tokens,omitempty"`
	// ChunkTokens, when positive, attaches the source chunks of the results up to this many tokens.
	ChunkTokens int `yaml:"chunk_tokens" json:"chunk_tokens,omitempty"`
}

// Recalled is a Recall result in a target-neutral shape; rag mode renders it as JSON into the answer
// prompt. The field names follow Hindsight's recall response, so the answer model sees the same shape
// whichever target produced it.
type Recalled struct {
	// Results are the recalled memories, best first.
	Results []Memory `json:"results"`
	// Chunks are the attached source chunks by id; empty unless RecallOptions.ChunkTokens was set.
	Chunks map[string]Chunk `json:"chunks,omitempty"`
}

// Texts returns the texts of the recalled memories in result order.
func (r Recalled) Texts() []string {
	texts := make([]string, 0, len(r.Results))
	for _, m := range r.Results {
		texts = append(texts, m.Text)
	}
	return texts
}

// Memory is one recalled memory. Times are RFC 3339 strings; empty = unknown.
type Memory struct {
	// ID is the memory id.
	ID string `json:"id,omitempty"`
	// Text is the fact or observation text.
	Text string `json:"text"`
	// Type is world, experience or observation.
	Type string `json:"type,omitempty"`
	// Entities are the names of the entities the memory mentions.
	Entities []string `json:"entities,omitempty"`
	// Context is the context given at retain.
	Context string `json:"context,omitempty"`
	// OccurredStart is when the fact started to hold.
	OccurredStart string `json:"occurred_start,omitempty"`
	// OccurredEnd is when the fact stopped holding.
	OccurredEnd string `json:"occurred_end,omitempty"`
	// MentionedAt is when the fact was mentioned (the retained session's time).
	MentionedAt string `json:"mentioned_at,omitempty"`
	// DocumentID is the document the memory was extracted from.
	DocumentID string `json:"document_id,omitempty"`
	// ChunkID is the chunk the memory was extracted from.
	ChunkID string `json:"chunk_id,omitempty"`
}

// Chunk is a slice of a retained document attached to recall results.
type Chunk struct {
	// ID is the chunk id.
	ID string `json:"id"`
	// Text is the chunk text, possibly cut to the chunk budget.
	Text string `json:"text"`
	// ChunkIndex is the chunk's position within its document.
	ChunkIndex int `json:"chunk_index"`
	// Truncated is true when Text was cut.
	Truncated bool `json:"truncated,omitempty"`
}
