package reflect

import (
	"context"
	"encoding/json"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// Tool names, as returned by Tool.Name and filtered by ReflectConfig.EnabledTools.
const (
	// ToolSearchMentalModels ranks mental-model pages for a query.
	ToolSearchMentalModels = "search_mental_models"
	// ToolReadMentalModels reads whole mental-model pages by id.
	ToolReadMentalModels = "read_mental_models"
	// ToolSearchObservations recalls observations only, optionally with entity state.
	ToolSearchObservations = "search_observations"
	// ToolRecall runs a full recall.
	ToolRecall = "recall"
	// ToolExpand reads the source facts and chunks behind retrieved units.
	ToolExpand = "expand"
	// ToolDone ends the loop with the answer and its citations.
	ToolDone = "done"
)

// Tool is one tool the reflect agent may call.
type Tool interface {
	// Name is the tool name: search_mental_models | read_mental_models | search_observations | recall |
	// expand | done.
	Name() string
	// Schema is the JSON Schema of the tool's arguments.
	Schema() json.RawMessage
	// Call runs the tool with the model's raw arguments, recording what it retrieved in st. A malformed
	// argument is returned to the model as a tool error; a retrieval failure is returned as an error.
	Call(ctx context.Context, st *AgentState, args json.RawMessage) (ToolResult, error)
}

// ToolResult is one tool call's outcome.
type ToolResult struct {
	// Output is the JSON sent back to the model as the tool message.
	Output json.RawMessage
	// Done reports that the loop should end (the done tool).
	Done bool
	// Answer is the final answer (done only).
	Answer string
	// Cited are the ids the answer cites (done only), filtered later to what was retrieved.
	Cited []string
	// Error is set when the call was malformed (bad JSON, a missing argument): Output then carries
	// {"error": Error} back to the model so it can fix the call, and the loop goes on.
	Error string
}

// AgentState is the mutable state of one reflect loop, shared by its tools.
type AgentState struct {
	// Request is the reflect request.
	Request ReflectRequest
	// Config is the effective config (bank < request).
	Config domain.BankConfig
	// QueryTime is the resolved reference time.
	QueryTime time.Time
	// Iteration is the current loop iteration, from 1.
	Iteration int
	// RetrievedUnits are the units any tool returned, by id; citations outside it are dropped.
	RetrievedUnits map[port.UnitID]domain.Unit
	// RetrievedModels are the mental models any tool returned.
	RetrievedModels map[port.MentalModelID]struct{}
	// Usage is per-slot token usage so far.
	Usage []domain.Usage
	// TotalTokens is the input + output tokens so far, checked against max_total_tokens.
	TotalTokens int64
	// ToolCalls are the calls made so far.
	ToolCalls []domain.ToolCallTrace
	// Searched reports that a retrieval tool ran successfully (even with no hits); done needs it when
	// require_evidence is on.
	Searched bool
}

// PageReader is what the page tools need from mental models. It uses only domain and port types so
// mentalmodel, which imports reflect, can provide it.
type PageReader interface {
	// SearchPages ranks the bank's pages for query (fused full-text and semantic), at most limit hits.
	SearchPages(ctx context.Context, b port.BankRef, query string, limit int) ([]domain.PageHit, error)
	// ReadPages returns the pages with the given ids, in order; unknown ids are skipped.
	ReadPages(ctx context.Context, b port.BankRef, ids []port.MentalModelID) ([]domain.MentalModel, error)
}
