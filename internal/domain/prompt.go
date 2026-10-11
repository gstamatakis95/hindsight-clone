package domain

// PromptOperation names an LLM operation whose prompt can be previewed.
type PromptOperation string

// Prompt operations, mirroring memory.v1.PreviewPromptRequest.PromptOperation.
const (
	// PromptOperationUnspecified is the zero value.
	PromptOperationUnspecified PromptOperation = ""
	// PromptOperationRetain is fact extraction.
	PromptOperationRetain PromptOperation = "retain"
	// PromptOperationConsolidation is the consolidation decision.
	PromptOperationConsolidation PromptOperation = "consolidation"
	// PromptOperationReflect is the reflect system prompt.
	PromptOperationReflect PromptOperation = "reflect"
	// PromptOperationMentalModelDelta is the delta-refresh prompt.
	PromptOperationMentalModelDelta PromptOperation = "mental_model_delta"
	// PromptOperationSynthesize is the single-shot synthesis prompt.
	PromptOperationSynthesize PromptOperation = "synthesize"
)

// PromptRole is a chat message role.
type PromptRole string

// Prompt roles, mirroring memory.v1.PromptMessage.PromptRole.
const (
	// PromptRoleUnspecified is the zero value.
	PromptRoleUnspecified PromptRole = ""
	// PromptRoleSystem is the system message.
	PromptRoleSystem PromptRole = "system"
	// PromptRoleUser is the user message.
	PromptRoleUser PromptRole = "user"
)

// PromptSource says where a prompt block's text came from.
type PromptSource string

// Prompt sources, mirroring memory.v1.PromptBlock.PromptSource.
const (
	// PromptSourceUnspecified is the zero value.
	PromptSourceUnspecified PromptSource = ""
	// PromptSourceBuiltin is a built-in template.
	PromptSourceBuiltin PromptSource = "builtin"
	// PromptSourceConfig is text from BankConfig (missions, custom prompt, directives).
	PromptSourceConfig PromptSource = "config"
)

// PromptBlock is one rendered block of a prompt message.
type PromptBlock struct {
	// Text is the rendered text; empty when inactive.
	Text string
	// Source is builtin or config.
	Source PromptSource
	// Field is the BankConfig field behind the block, if any.
	Field string
	// Section names the block (disposition, directives, ...).
	Section string
	// Active reports whether the block is sent.
	Active bool
}

// PromptMessage is one rendered message.
type PromptMessage struct {
	// Role is the message role.
	Role PromptRole
	// Blocks are the message's blocks in order.
	Blocks []PromptBlock
}

// PromptPreview is the exact set of messages an operation would send, with no LLM call.
type PromptPreview struct {
	// Messages are the rendered messages.
	Messages []PromptMessage
	// ResponseSchemaJSON is the JSON Schema the call would request.
	ResponseSchemaJSON string
	// SkippedReason explains why the operation would make no call (e.g. chunks mode).
	SkippedReason string
}
