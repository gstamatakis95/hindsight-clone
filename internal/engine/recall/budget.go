package recall

import (
	"math"
	"memd/internal/domain"
)

// adaptiveReference is the max_tokens at which the adaptive budget equals the fixed one: Hindsight's adaptive
// ratios (0.025 / 0.075 / 0.25 of max_tokens) were chosen to match 100 / 300 / 1000 at 4096 tokens.
const adaptiveReference = 4096

// budgetLimit returns the per-arm candidate limit for budget b (unspecified = MID). The fixed function returns
// budget_low / budget_mid / budget_high (default 100 / 300 / 1000) as configured. The adaptive function scales
// that level by maxTokens / 4096 (maxTokens <= 0 = DefaultMaxTokens) and clamps the result to
// [budget_min, budget_max] (default [20, 2000]). The result is at least 1.
func budgetLimit(cfg domain.RecallConfig, b domain.Budget, maxTokens int) int {
	level := budgetLevel(cfg, b)
	if cfg.BudgetFunction != domain.BudgetFunctionAdaptive {
		return max(level, 1)
	}
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	limit := int(math.Round(float64(level) * float64(maxTokens) / adaptiveReference))
	lo, hi := intOr(cfg.BudgetMin, defaultBudgetMin), intOr(cfg.BudgetMax, defaultBudgetMax)
	if hi < lo {
		hi = lo
	}
	return max(min(max(limit, lo), hi), 1)
}

// budgetLevel returns the configured fixed limit of budget b.
func budgetLevel(cfg domain.RecallConfig, b domain.Budget) int {
	switch b {
	case domain.BudgetLow:
		return intOr(cfg.BudgetLow, defaultBudgetLow)
	case domain.BudgetHigh:
		return intOr(cfg.BudgetHigh, defaultBudgetHigh)
	default:
		return intOr(cfg.BudgetMid, defaultBudgetMid)
	}
}
