package glmoptimizer

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestBudgetPolicyForPlan(t *testing.T) {
	tests := []struct {
		name       string
		maxOutput  int
		minVisible int
	}{
		{name: "basic", maxOutput: 8192, minVisible: 1024},
		{name: "standard", maxOutput: 16384, minVisible: 2048},
		{name: "pro", maxOutput: 32768, minVisible: 4096},
		{name: "small plans keep one thousand visible", maxOutput: 4096, minVisible: 1024},
		{name: "large plans cap the initial reserve", maxOutput: 65536, minVisible: 4096},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := BudgetPolicyForPlan(test.name, test.maxOutput)
			if policy.MaxOutputTokens != test.maxOutput {
				t.Fatalf("MaxOutputTokens = %d, want %d", policy.MaxOutputTokens, test.maxOutput)
			}
			if policy.MinVisibleTokens != test.minVisible {
				t.Fatalf("MinVisibleTokens = %d, want %d", policy.MinVisibleTokens, test.minVisible)
			}
		})
	}
}

func TestApplyBudgetAcceptsSupportedCombinations(t *testing.T) {
	tests := []struct {
		name             string
		body             string
		policy           BudgetPolicy
		wantMax          int
		wantEffort       string
		wantReasoningMax int
		wantVisible      int
	}{
		{
			name:        "reasoning disabled preserves state and injects plan cap",
			body:        `{"model":"glm-5.2","messages":[],"reasoning":{"enabled":false},"custom":{"keep":true}}`,
			policy:      BudgetPolicyForPlan("basic", 8192),
			wantMax:     8192,
			wantVisible: 8192,
		},
		{
			name:        "enabled without effort normalizes to high",
			body:        `{"model":"glm-5.2","messages":[],"reasoning":{"enabled":true}}`,
			policy:      BudgetPolicyForPlan("basic", 8192),
			wantMax:     8192,
			wantEffort:  "high",
			wantVisible: 1638,
		},
		{
			name:        "explicit high within standard reserve",
			body:        `{"model":"glm-5.2","messages":[],"max_tokens":12000,"reasoning":{"enabled":true,"effort":"high"}}`,
			policy:      BudgetPolicyForPlan("standard", 16384),
			wantMax:     12000,
			wantEffort:  "high",
			wantVisible: 2400,
		},
		{
			name:             "explicit reasoning max at pro boundary",
			body:             `{"model":"glm-5.2","messages":[],"max_tokens":32768,"reasoning":{"enabled":true,"max_tokens":28672}}`,
			policy:           BudgetPolicyForPlan("pro", 32768),
			wantMax:          32768,
			wantEffort:       "high",
			wantReasoningMax: 28672,
			wantVisible:      4096,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, decision, err := ApplyBudget([]byte(test.body), test.policy)
			if err != nil {
				t.Fatalf("ApplyBudget: %v", err)
			}
			if decision.EffectiveMaxTokens != test.wantMax {
				t.Fatalf("EffectiveMaxTokens = %d, want %d", decision.EffectiveMaxTokens, test.wantMax)
			}
			if decision.EffectiveEffort != test.wantEffort {
				t.Fatalf("EffectiveEffort = %q, want %q", decision.EffectiveEffort, test.wantEffort)
			}
			if decision.ReasoningMaxTokens != test.wantReasoningMax {
				t.Fatalf("ReasoningMaxTokens = %d, want %d", decision.ReasoningMaxTokens, test.wantReasoningMax)
			}
			if decision.EstimatedVisibleTokens != test.wantVisible {
				t.Fatalf("EstimatedVisibleTokens = %d, want %d", decision.EstimatedVisibleTokens, test.wantVisible)
			}

			var got map[string]json.RawMessage
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode result: %v", err)
			}
			var maxTokens int
			if err := json.Unmarshal(got["max_tokens"], &maxTokens); err != nil || maxTokens != test.wantMax {
				t.Fatalf("encoded max_tokens = %d, err=%v, want %d", maxTokens, err, test.wantMax)
			}
			if test.name == "reasoning disabled preserves state and injects plan cap" {
				if _, ok := got["custom"]; !ok {
					t.Fatal("unknown request field was dropped")
				}
			}
		})
	}
}

func TestApplyBudgetRejectsUnsafeCombinations(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		policy   BudgetPolicy
		wantCode string
	}{
		{
			name:     "xhigh is unavailable in v1",
			body:     `{"model":"glm-5.2","messages":[],"max_tokens":8192,"reasoning":{"enabled":true,"effort":"xhigh"}}`,
			policy:   BudgetPolicyForPlan("basic", 8192),
			wantCode: BudgetCodeXHighUnsupported,
		},
		{
			name:     "high cannot consume basic visible reserve",
			body:     `{"model":"glm-5.2","messages":[],"max_tokens":4096,"reasoning":{"enabled":true,"effort":"high"}}`,
			policy:   BudgetPolicyForPlan("basic", 8192),
			wantCode: BudgetCodeVisibleReserve,
		},
		{
			name:     "explicit reasoning max exceeds reserve",
			body:     `{"model":"glm-5.2","messages":[],"max_tokens":32768,"reasoning":{"enabled":true,"max_tokens":28673}}`,
			policy:   BudgetPolicyForPlan("pro", 32768),
			wantCode: BudgetCodeVisibleReserve,
		},
		{
			name:     "request exceeds plan hard cap",
			body:     `{"model":"glm-5.2","messages":[],"max_tokens":8193}`,
			policy:   BudgetPolicyForPlan("basic", 8192),
			wantCode: BudgetCodePlanLimit,
		},
		{
			name:     "fractional max tokens is invalid",
			body:     `{"model":"glm-5.2","messages":[],"max_tokens":8191.5}`,
			policy:   BudgetPolicyForPlan("basic", 8192),
			wantCode: BudgetCodeInvalidRequest,
		},
		{
			name:     "disabled reasoning cannot carry active budget fields",
			body:     `{"model":"glm-5.2","messages":[],"reasoning":{"enabled":false,"effort":"high"}}`,
			policy:   BudgetPolicyForPlan("basic", 8192),
			wantCode: BudgetCodeInvalidRequest,
		},
		{
			name:     "null reasoning enabled is invalid",
			body:     `{"model":"glm-5.2","messages":[],"reasoning":{"enabled":null}}`,
			policy:   BudgetPolicyForPlan("basic", 8192),
			wantCode: BudgetCodeInvalidRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := ApplyBudget([]byte(test.body), test.policy)
			var budgetErr *BudgetError
			if !errors.As(err, &budgetErr) {
				t.Fatalf("error = %v, want *BudgetError", err)
			}
			if budgetErr.Code != test.wantCode {
				t.Fatalf("error code = %q, want %q", budgetErr.Code, test.wantCode)
			}
			if budgetErr.HTTPStatus != 400 {
				t.Fatalf("HTTP status = %d, want 400", budgetErr.HTTPStatus)
			}
		})
	}
}

func TestApplyBudgetRejectsInvalidPolicyAsServerError(t *testing.T) {
	_, _, err := ApplyBudget(
		[]byte(`{"model":"glm-5.2","messages":[]}`),
		BudgetPolicy{PlanName: "broken"},
	)
	var budgetErr *BudgetError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("error = %v, want *BudgetError", err)
	}
	if budgetErr.HTTPStatus != 500 || budgetErr.Code != BudgetCodePolicyInvalid {
		t.Fatalf("error = %#v, want policy-invalid 500", budgetErr)
	}
}
