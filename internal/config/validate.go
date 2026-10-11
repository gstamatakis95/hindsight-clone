package config

import (
	"fmt"
	"memd/internal/domain"
	"strings"
)

// RPC names accepted by ValidateOverrides: the gRPC methods that take a BankConfig, plus the two
// config-shaped writes that are not per-request overrides.
const (
	// RPCRetain is MemoryService.Retain, for both the request and per-item overrides.
	RPCRetain = "Retain"
	// RPCRecall is MemoryService.Recall.
	RPCRecall = "Recall"
	// RPCReflect is MemoryService.Reflect.
	RPCReflect = "Reflect"
	// RPCConsolidate is ObservationService.Consolidate.
	RPCConsolidate = "Consolidate"
	// RPCReprocessDocument is DocumentService.ReprocessDocument (same reach as Retain).
	RPCReprocessDocument = "ReprocessDocument"
	// RPCUpdateMemory is MemoryUnitService.UpdateMemory (kept on the rederive operation).
	RPCUpdateMemory = "UpdateMemory"
	// RPCRefreshMentalModel is MentalModelService.RefreshMentalModel.
	RPCRefreshMentalModel = "RefreshMentalModel"
	// RPCMentalModelSpec is MentalModelSpec.overrides on CreateMentalModel / UpdateMentalModel (same reach
	// as RefreshMentalModel).
	RPCMentalModelSpec = "MentalModelSpec"
	// RPCBankConfig is a whole config level: CreateBank, UpdateBankConfig, UpdateTenantConfig and bank
	// templates. Every field is allowed; only slot rules apply.
	RPCBankConfig = "BankConfig"
)

// allowlist says which config paths and model slots an RPC can use. A path entry allows itself and,
// for a nested message, every path under it.
type allowlist struct {
	all   bool
	paths []string
	slots []domain.ModelSlot
}

var (
	retainAllow = allowlist{
		paths: []string{
			"retain_mission", "extraction_mode", "custom_extraction_prompt", "entity_labels",
			"entities_allow_free_form", "default_observation_scopes", "retain",
		},
		slots: []domain.ModelSlot{domain.ModelSlotExtract, domain.ModelSlotTimeParse},
	}
	refreshAllow = allowlist{
		paths: []string{"reflect_mission", "disposition", "reflect", "mental_models"},
		slots: []domain.ModelSlot{
			domain.ModelSlotReflect, domain.ModelSlotSynthesize, domain.ModelSlotMentalModelDelta,
			domain.ModelSlotStructuredOutput,
		},
	}
)

// allowlists is the per-RPC table of SPEC "Per-request overrides" (decision 401).
var allowlists = map[string]allowlist{
	RPCRetain:            retainAllow,
	RPCReprocessDocument: retainAllow,
	RPCRecall: {
		paths: []string{"recall"},
		slots: []domain.ModelSlot{domain.ModelSlotTimeParse, domain.ModelSlotRerank},
	},
	RPCReflect: {
		paths: []string{"reflect_mission", "disposition", "reflect", "recall"},
		slots: []domain.ModelSlot{domain.ModelSlotReflect, domain.ModelSlotStructuredOutput, domain.ModelSlotRerank},
	},
	RPCConsolidate: {
		paths: []string{
			"observations_mission", "max_observations_per_scope", "consolidation_source_facts_max_tokens",
			"consolidation_source_facts_max_tokens_per_observation", "consolidation_llm_batch_size",
			"consolidation_max_units_per_run", "dedup_threshold", "consolidation_strategies",
		},
		slots: []domain.ModelSlot{domain.ModelSlotConsolidate, domain.ModelSlotDedupCheck},
	},
	RPCUpdateMemory: {
		paths: []string{"retain.entity_match_threshold"},
		slots: []domain.ModelSlot{domain.ModelSlotExtract, domain.ModelSlotTimeParse},
	},
	RPCRefreshMentalModel: refreshAllow,
	RPCMentalModelSpec:    refreshAllow,
	RPCBankConfig:         {all: true},
}

// strategySlots are the only slots a consolidation strategy may bind.
var strategySlots = []domain.ModelSlot{domain.ModelSlotConsolidate, domain.ModelSlotDedupCheck}

// ValidateOverrides rejects, with domain.ErrInvalidArgument, any field the RPC can't use (for example
// retain_mission on Recall), so an override is never silently ignored. Everywhere it also rejects a model
// binding for EMBED (server-wide), an unspecified or unknown slot, a slot bound twice in one level, a binding
// with no model name, a consolidation strategy binding a slot other than CONSOLIDATE / DEDUP_CHECK, and a
// disposition trait outside 0..5.
// An unknown rpc is domain.ErrInvalidArgument too.
func ValidateOverrides(rpc string, o domain.BankConfig) error {
	al, ok := allowlists[rpc]
	if !ok {
		return fmt.Errorf("%w: unknown rpc %q for overrides", domain.ErrInvalidArgument, rpc)
	}
	if err := checkSlots(o.Models, al); err != nil {
		return fmt.Errorf("%w: %s overrides: models: %w", domain.ErrInvalidArgument, rpc, err)
	}
	for i, s := range o.ConsolidationStrategies {
		if err := checkSlots(s.Models, allowlist{slots: strategySlots}); err != nil {
			return fmt.Errorf("%w: %s overrides: consolidation_strategies[%d].models: %w",
				domain.ErrInvalidArgument, rpc, i, err)
		}
	}
	if err := checkDisposition(o.Disposition); err != nil {
		return fmt.Errorf("%w: %s overrides: %w", domain.ErrInvalidArgument, rpc, err)
	}
	if al.all {
		return nil
	}
	for _, p := range setPaths(o) {
		if p != "models" && !al.allowsPath(p) {
			return fmt.Errorf("%w: %s can't use override %q", domain.ErrInvalidArgument, rpc, p)
		}
	}
	return nil
}

// PreviewRPC maps a PreviewPrompt operation to the RPC whose overrides it previews, so the server can
// validate PreviewPromptRequest.overrides with ValidateOverrides(PreviewRPC(op), o). Unknown = "".
func PreviewRPC(op domain.PromptOperation) string {
	switch op {
	case domain.PromptOperationRetain:
		return RPCRetain
	case domain.PromptOperationConsolidation:
		return RPCConsolidate
	case domain.PromptOperationReflect:
		return RPCReflect
	case domain.PromptOperationMentalModelDelta, domain.PromptOperationSynthesize:
		return RPCRefreshMentalModel
	default:
		return ""
	}
}

func (a allowlist) allowsPath(p string) bool {
	for _, allowed := range a.paths {
		if p == allowed || strings.HasPrefix(p, allowed+".") {
			return true
		}
	}
	return false
}

func (a allowlist) allowsSlot(s domain.ModelSlot) bool {
	if a.all {
		return true
	}
	for _, allowed := range a.slots {
		if s == allowed {
			return true
		}
	}
	return false
}

// checkDisposition requires each trait in 0..5 (0 = unset, inherit; 1-5 per the spec).
func checkDisposition(d *domain.Disposition) error {
	if d == nil {
		return nil
	}
	for name, v := range map[string]int{"skepticism": d.Skepticism, "literalism": d.Literalism, "empathy": d.Empathy} {
		if v < 0 || v > 5 {
			return fmt.Errorf("disposition.%s = %d, want 1-5 (0 = unset)", name, v)
		}
	}
	return nil
}

// checkSlots validates one level's slot bindings against a.
func checkSlots(models []domain.SlotBinding, a allowlist) error {
	seen := map[domain.ModelSlot]bool{}
	for _, sb := range models {
		switch {
		case sb.Slot == domain.ModelSlotUnspecified || sb.Slot == domain.ModelSlotEmbed:
			return fmt.Errorf("slot %q can't be bound per bank or request", sb.Slot)
		case !(allowlist{slots: bindableSlots}).allowsSlot(sb.Slot):
			return fmt.Errorf("unknown slot %q", sb.Slot)
		case seen[sb.Slot]:
			return fmt.Errorf("slot %q bound twice", sb.Slot)
		case !a.allowsSlot(sb.Slot):
			return fmt.Errorf("slot %q not allowed here", sb.Slot)
		case sb.Binding.Model == "":
			return fmt.Errorf("slot %q has no model name", sb.Slot)
		}
		seen[sb.Slot] = true
	}
	return nil
}
