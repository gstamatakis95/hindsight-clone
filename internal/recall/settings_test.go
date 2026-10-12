package recall

import (
	"memd/internal/domain"
	"testing"
)

func TestBudgetLimit(t *testing.T) {
	adaptive := domain.RecallConfig{BudgetFunction: domain.BudgetFunctionAdaptive}
	custom := domain.RecallConfig{
		BudgetFunction: domain.BudgetFunctionAdaptive, BudgetMid: ptr(400), BudgetMin: ptr(50), BudgetMax: ptr(500),
	}
	inverted := domain.RecallConfig{BudgetFunction: domain.BudgetFunctionAdaptive, BudgetMin: ptr(30), BudgetMax: ptr(10)}
	tests := []struct {
		name      string
		cfg       domain.RecallConfig
		budget    domain.Budget
		maxTokens int
		want      int
	}{
		{"fixed low default", domain.RecallConfig{}, domain.BudgetLow, 0, 100},
		{"fixed mid default", domain.RecallConfig{}, domain.BudgetMid, 0, 300},
		{"fixed unspecified is mid", domain.RecallConfig{}, domain.BudgetUnspecified, 0, 300},
		{"fixed high default", domain.RecallConfig{}, domain.BudgetHigh, 0, 1000},
		{"fixed ignores max_tokens and clamp", domain.RecallConfig{
			BudgetFunction: domain.BudgetFunctionFixed, BudgetHigh: ptr(5000), BudgetMax: ptr(2000),
		}, domain.BudgetHigh, 10, 5000},
		{"fixed floor 1", domain.RecallConfig{BudgetLow: ptr(0)}, domain.BudgetLow, 0, 1},
		{"adaptive at 4096 equals fixed", adaptive, domain.BudgetMid, 4096, 300},
		{"adaptive max_tokens 0 = default 4096", adaptive, domain.BudgetHigh, 0, 1000},
		{"adaptive scales", adaptive, domain.BudgetMid, 8192, 600},
		{"adaptive clamps to budget_min", adaptive, domain.BudgetLow, 100, 20},
		{"adaptive clamps to budget_max", adaptive, domain.BudgetHigh, 100000, 2000},
		{"adaptive custom clamp low", custom, domain.BudgetMid, 100, 50},
		{"adaptive custom clamp high", custom, domain.BudgetMid, 40960, 500},
		{"adaptive custom inside", custom, domain.BudgetMid, 4096, 400},
		{"adaptive inverted clamp uses min", inverted, domain.BudgetMid, 4096, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := budgetLimit(tt.cfg, tt.budget, tt.maxTokens); got != tt.want {
				t.Fatalf("budgetLimit = %d, want %d", got, tt.want)
			}
		})
	}
}
