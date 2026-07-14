package glmoptimizer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

const (
	BudgetCodeInvalidRequest   = "GLM52_BUDGET_INVALID"
	BudgetCodePlanLimit        = "GLM52_PLAN_OUTPUT_LIMIT"
	BudgetCodeVisibleReserve   = "GLM52_VISIBLE_OUTPUT_RESERVE"
	BudgetCodeXHighUnsupported = "GLM52_XHIGH_UNSUPPORTED"
	BudgetCodePolicyInvalid    = "GLM52_BUDGET_POLICY_INVALID"
)

// BudgetPolicy contains plan data needed by the GLM boundary. It deliberately
// does not contain request text, credentials, or any other value that could be
// leaked by budget-decision logging.
type BudgetPolicy struct {
	PlanName          string
	MaxOutputTokens   int
	MinVisibleTokens  int
	HighReasoningRate float64
}

// BudgetDecision is safe to write as structured telemetry. RequestedMaxTokens
// is zero when the client omitted max_tokens and the plan cap was injected.
type BudgetDecision struct {
	PlanName                 string `json:"plan_name"`
	RequestedMaxTokens       int    `json:"requested_max_tokens"`
	EffectiveMaxTokens       int    `json:"effective_max_tokens"`
	ReasoningEnabled         bool   `json:"reasoning_enabled"`
	RequestedEffort          string `json:"requested_effort,omitempty"`
	EffectiveEffort          string `json:"effective_effort,omitempty"`
	ReasoningMaxTokens       int    `json:"reasoning_max_tokens,omitempty"`
	MinVisibleTokens         int    `json:"min_visible_tokens"`
	EstimatedReasoningTokens int    `json:"estimated_reasoning_tokens"`
	EstimatedVisibleTokens   int    `json:"estimated_visible_tokens"`
	Reason                   string `json:"reason"`
}

// BudgetError maps directly to an OpenAI-compatible structured API error.
// Details contains only token counts and plan metadata, never prompt content.
type BudgetError struct {
	HTTPStatus int            `json:"-"`
	Code       string         `json:"code"`
	Message    string         `json:"message"`
	Details    BudgetDecision `json:"details"`
}

func (e *BudgetError) Error() string {
	return e.Message
}

// BudgetPolicyForPlan derives the V0.2.1 reserve agreed after Phase 0A: one
// eighth of the plan output cap, bounded to 1K..4K visible tokens. The 80%
// high-reasoning ratio is a conservative admission estimate, not a provider
// guarantee and is never advertised as one.
func BudgetPolicyForPlan(planName string, maxOutputTokens int) BudgetPolicy {
	minVisible := maxOutputTokens / 8
	if minVisible < 1024 {
		minVisible = 1024
	}
	if minVisible > 4096 {
		minVisible = 4096
	}
	return BudgetPolicy{
		PlanName:          planName,
		MaxOutputTokens:   maxOutputTokens,
		MinVisibleTokens:  minVisible,
		HighReasoningRate: 0.80,
	}
}

// ApplyBudget validates and normalizes the final GLM request before routing.
// It never raises a plan cap, silently downgrades effort, or rewrites a client
// supplied budget. Unknown provider fields are preserved.
func ApplyBudget(body []byte, policy BudgetPolicy) ([]byte, BudgetDecision, error) {
	decision := BudgetDecision{
		PlanName:         policy.PlanName,
		MinVisibleTokens: policy.MinVisibleTokens,
	}
	if policy.MaxOutputTokens <= 0 || policy.MinVisibleTokens <= 0 ||
		policy.MinVisibleTokens >= policy.MaxOutputTokens ||
		policy.HighReasoningRate <= 0 || policy.HighReasoningRate >= 1 {
		decision.Reason = "invalid_policy"
		return nil, decision, budgetError(500, BudgetCodePolicyInvalid,
			"GLM-5.2 套餐输出预算未正确配置", decision)
	}

	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		decision.Reason = "malformed_request"
		return nil, decision, budgetError(400, BudgetCodeInvalidRequest,
			fmt.Sprintf("GLM-5.2 请求格式错误: %v", err), decision)
	}

	effectiveMax := policy.MaxOutputTokens
	if raw, ok := request["max_tokens"]; ok {
		requested, err := decodePositiveInt(raw, "max_tokens")
		if err != nil {
			decision.Reason = "invalid_max_tokens"
			return nil, decision, budgetError(400, BudgetCodeInvalidRequest, err.Error(), decision)
		}
		decision.RequestedMaxTokens = requested
		if requested > policy.MaxOutputTokens {
			decision.EffectiveMaxTokens = policy.MaxOutputTokens
			decision.Reason = "plan_limit_exceeded"
			return nil, decision, budgetError(400, BudgetCodePlanLimit,
				fmt.Sprintf("max_tokens=%d 超过套餐上限 %d；请降低 max_tokens 或升级套餐", requested, policy.MaxOutputTokens), decision)
		}
		effectiveMax = requested
	} else {
		encoded, _ := json.Marshal(effectiveMax)
		request["max_tokens"] = encoded
	}
	decision.EffectiveMaxTokens = effectiveMax
	decision.EstimatedVisibleTokens = effectiveMax

	reasoning, reasoningPresent, err := decodeReasoning(request["reasoning"])
	if err != nil {
		decision.Reason = "invalid_reasoning"
		return nil, decision, budgetError(400, BudgetCodeInvalidRequest, err.Error(), decision)
	}

	enabled := false
	enabledSpecified := false
	if raw, ok := reasoning["enabled"]; ok {
		enabledSpecified = true
		trimmed := string(bytes.TrimSpace(raw))
		if trimmed != "true" && trimmed != "false" {
			decision.Reason = "invalid_reasoning_enabled"
			return nil, decision, budgetError(400, BudgetCodeInvalidRequest,
				"reasoning.enabled 必须是布尔值", decision)
		}
		enabled = trimmed == "true"
	}

	effort := ""
	if raw, ok := reasoning["effort"]; ok {
		if err := json.Unmarshal(raw, &effort); err != nil || strings.TrimSpace(effort) == "" {
			decision.Reason = "invalid_reasoning_effort"
			return nil, decision, budgetError(400, BudgetCodeInvalidRequest,
				"reasoning.effort 必须是非空字符串", decision)
		}
		effort = strings.ToLower(strings.TrimSpace(effort))
		decision.RequestedEffort = effort
		if !enabledSpecified {
			enabled = true
		}
	}

	reasoningMax := 0
	if raw, ok := reasoning["max_tokens"]; ok {
		reasoningMax, err = decodePositiveInt(raw, "reasoning.max_tokens")
		if err != nil {
			decision.Reason = "invalid_reasoning_max_tokens"
			return nil, decision, budgetError(400, BudgetCodeInvalidRequest, err.Error(), decision)
		}
		decision.ReasoningMaxTokens = reasoningMax
		if !enabledSpecified {
			enabled = true
		}
	}
	decision.ReasoningEnabled = enabled
	if enabledSpecified && !enabled && (effort != "" || reasoningMax > 0) {
		decision.Reason = "conflicting_reasoning_fields"
		return nil, decision, budgetError(400, BudgetCodeInvalidRequest,
			"reasoning.enabled=false 不能同时设置 effort 或 reasoning.max_tokens", decision)
	}

	if enabled {
		if effort == "" {
			effort = "high"
			encoded, _ := json.Marshal(effort)
			reasoning["effort"] = encoded
		}
		decision.EffectiveEffort = effort
		if effort == "xhigh" {
			decision.Reason = "xhigh_unsupported"
			return nil, decision, budgetError(400, BudgetCodeXHighUnsupported,
				"GLM-5.2 V1 暂不支持 reasoning.effort=xhigh；请使用 high", decision)
		}
		if effort != "low" && effort != "medium" && effort != "high" {
			decision.Reason = "unsupported_reasoning_effort"
			return nil, decision, budgetError(400, BudgetCodeInvalidRequest,
				"reasoning.effort 仅支持 low、medium 或 high", decision)
		}

		if reasoningMax > 0 {
			decision.EstimatedReasoningTokens = reasoningMax
			decision.EstimatedVisibleTokens = effectiveMax - reasoningMax
		} else {
			// Until per-effort coefficients have enough production evidence, use
			// the conservative high estimate for every enabled effort.
			decision.EstimatedReasoningTokens = int(math.Ceil(float64(effectiveMax) * policy.HighReasoningRate))
			decision.EstimatedVisibleTokens = effectiveMax - decision.EstimatedReasoningTokens
		}
		if decision.EstimatedVisibleTokens < policy.MinVisibleTokens {
			decision.Reason = "visible_reserve_not_met"
			return nil, decision, budgetError(400, BudgetCodeVisibleReserve,
				fmt.Sprintf("当前预算预计仅保留 %d 个可见输出 token，低于套餐安全保留量 %d；请提高 max_tokens 或降低 reasoning 预算",
					decision.EstimatedVisibleTokens, policy.MinVisibleTokens), decision)
		}
	}

	if reasoningPresent || enabled {
		encoded, err := json.Marshal(reasoning)
		if err != nil {
			decision.Reason = "encode_reasoning_failed"
			return nil, decision, budgetError(500, BudgetCodePolicyInvalid,
				"GLM-5.2 reasoning 预算编码失败", decision)
		}
		request["reasoning"] = encoded
	}
	result, err := json.Marshal(request)
	if err != nil {
		decision.Reason = "encode_request_failed"
		return nil, decision, budgetError(500, BudgetCodePolicyInvalid,
			"GLM-5.2 请求预算编码失败", decision)
	}
	decision.Reason = "accepted"
	return result, decision, nil
}

func decodeReasoning(raw json.RawMessage) (map[string]json.RawMessage, bool, error) {
	if len(raw) == 0 {
		return make(map[string]json.RawMessage), false, nil
	}
	var reasoning map[string]json.RawMessage
	if err := json.Unmarshal(raw, &reasoning); err != nil || reasoning == nil {
		return nil, true, fmt.Errorf("reasoning 必须是 JSON 对象")
	}
	return reasoning, true, nil
}

func decodePositiveInt(raw json.RawMessage, field string) (int, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return 0, fmt.Errorf("%s 必须是正整数", field)
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("%s 必须是正整数", field)
	}
	integer, err := number.Int64()
	if err != nil || integer <= 0 || int64(int(integer)) != integer {
		return 0, fmt.Errorf("%s 必须是正整数", field)
	}
	return int(integer), nil
}

func budgetError(status int, code, message string, decision BudgetDecision) *BudgetError {
	return &BudgetError{
		HTTPStatus: status,
		Code:       code,
		Message:    message,
		Details:    decision,
	}
}
