package glmoptimizer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type TerminalState string

const (
	TerminalSuccessContent       TerminalState = "success_content"
	TerminalSuccessToolCall      TerminalState = "success_tool_call"
	TerminalSuccessRefusal       TerminalState = "success_refusal"
	TerminalFailureReasoningOnly TerminalState = "failure_reasoning_only"
	TerminalFailureEmpty         TerminalState = "failure_empty"
	TerminalFailureMalformed     TerminalState = "failure_malformed"

	TerminalCodeReasoningOnly = "GLM52_REASONING_ONLY"
	TerminalCodeEmpty         = "GLM52_EMPTY_COMPLETION"
	TerminalCodeMalformed     = "GLM52_MALFORMED_COMPLETION"
)

// TerminalOutcome is the structured result of inspecting a non-streaming
// completion. Detail is intentionally limited to protocol diagnostics and
// must never include response content, reasoning text, or tool arguments.
type TerminalOutcome struct {
	State         TerminalState `json:"state"`
	ErrorCode     string        `json:"error_code,omitempty"`
	Consumable    bool          `json:"consumable"`
	Retryable     bool          `json:"retryable"`
	ChoiceIndex   int           `json:"choice_index"`
	ToolCallCount int           `json:"tool_call_count,omitempty"`
	FinishReason  string        `json:"finish_reason,omitempty"`
	Detail        string        `json:"detail,omitempty"`
}

type completionEnvelope struct {
	Choices []choiceEnvelope `json:"choices"`
}

type choiceEnvelope struct {
	Index        int              `json:"index"`
	Message      *messageEnvelope `json:"message"`
	FinishReason *string          `json:"finish_reason"`
}

type messageEnvelope struct {
	Content          json.RawMessage `json:"content"`
	Refusal          json.RawMessage `json:"refusal"`
	Reasoning        json.RawMessage `json:"reasoning"`
	ReasoningDetails json.RawMessage `json:"reasoning_details"`
	ToolCalls        json.RawMessage `json:"tool_calls"`
}

type toolCallEnvelope struct {
	ID       string                `json:"id"`
	Function *toolFunctionEnvelope `json:"function"`
}

type toolFunctionEnvelope struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ClassifyNonStream classifies a complete, HTTP-200 non-streaming response.
// Non-consumable results are retryable because no bytes have been committed to
// the client yet; the caller still owns the one-retry/same-model boundary.
func ClassifyNonStream(body []byte) TerminalOutcome {
	var response completionEnvelope
	if err := json.Unmarshal(body, &response); err != nil {
		return malformedOutcome(fmt.Sprintf("invalid completion JSON: %T", err))
	}
	if len(response.Choices) == 0 {
		return TerminalOutcome{
			State:       TerminalFailureEmpty,
			ErrorCode:   TerminalCodeEmpty,
			Retryable:   true,
			ChoiceIndex: -1,
			Detail:      "completion has no choices",
		}
	}

	var contentCandidate *TerminalOutcome
	var toolCandidate *TerminalOutcome
	var refusalCandidate *TerminalOutcome
	hasReasoning := false

	for position, choice := range response.Choices {
		if choice.Message == nil {
			return malformedOutcome(fmt.Sprintf("choices[%d].message is missing", position))
		}
		finishReason := ""
		if choice.FinishReason != nil {
			finishReason = *choice.FinishReason
		}

		content, err := optionalText(choice.Message.Content, fmt.Sprintf("choices[%d].message.content", position))
		if err != nil {
			return malformedOutcome(err.Error())
		}
		refusal, err := optionalText(choice.Message.Refusal, fmt.Sprintf("choices[%d].message.refusal", position))
		if err != nil {
			return malformedOutcome(err.Error())
		}
		toolCount, err := validateToolCalls(choice.Message.ToolCalls, position)
		if err != nil {
			return malformedOutcome(err.Error())
		}

		if content != "" && contentCandidate == nil {
			candidate := successOutcome(TerminalSuccessContent, choice.Index, finishReason)
			contentCandidate = &candidate
		}
		if toolCount > 0 && toolCandidate == nil {
			candidate := successOutcome(TerminalSuccessToolCall, choice.Index, finishReason)
			candidate.ToolCallCount = toolCount
			toolCandidate = &candidate
		}
		if refusal != "" && refusalCandidate == nil {
			candidate := successOutcome(TerminalSuccessRefusal, choice.Index, finishReason)
			refusalCandidate = &candidate
		}
		if rawHasValue(choice.Message.Reasoning) || rawHasValue(choice.Message.ReasoningDetails) {
			hasReasoning = true
		}
	}

	if contentCandidate != nil {
		return *contentCandidate
	}
	if toolCandidate != nil {
		return *toolCandidate
	}
	if refusalCandidate != nil {
		return *refusalCandidate
	}
	if hasReasoning {
		return TerminalOutcome{
			State:       TerminalFailureReasoningOnly,
			ErrorCode:   TerminalCodeReasoningOnly,
			Retryable:   true,
			ChoiceIndex: -1,
			Detail:      "completion contains reasoning but no consumable artifact",
		}
	}
	return TerminalOutcome{
		State:       TerminalFailureEmpty,
		ErrorCode:   TerminalCodeEmpty,
		Retryable:   true,
		ChoiceIndex: -1,
		Detail:      "completion contains no consumable artifact",
	}
}

func successOutcome(state TerminalState, choiceIndex int, finishReason string) TerminalOutcome {
	return TerminalOutcome{
		State:        state,
		Consumable:   true,
		ChoiceIndex:  choiceIndex,
		FinishReason: finishReason,
	}
}

func malformedOutcome(detail string) TerminalOutcome {
	return TerminalOutcome{
		State:       TerminalFailureMalformed,
		ErrorCode:   TerminalCodeMalformed,
		Retryable:   true,
		ChoiceIndex: -1,
		Detail:      detail,
	}
}

func optionalText(raw json.RawMessage, field string) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err != nil {
		return "", fmt.Errorf("%s must be a string or null", field)
	}
	return strings.TrimSpace(text), nil
}

func validateToolCalls(raw json.RawMessage, choicePosition int) (int, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, nil
	}
	var calls []toolCallEnvelope
	if err := json.Unmarshal(trimmed, &calls); err != nil {
		return 0, fmt.Errorf("choices[%d].message.tool_calls must be an array", choicePosition)
	}
	for callPosition, call := range calls {
		path := fmt.Sprintf("choices[%d].message.tool_calls[%d]", choicePosition, callPosition)
		if strings.TrimSpace(call.ID) == "" {
			return 0, fmt.Errorf("%s.id is missing", path)
		}
		if call.Function == nil {
			return 0, fmt.Errorf("%s.function is missing", path)
		}
		if strings.TrimSpace(call.Function.Name) == "" {
			return 0, fmt.Errorf("%s.function.name is missing", path)
		}
		var arguments string
		if len(call.Function.Arguments) == 0 || json.Unmarshal(call.Function.Arguments, &arguments) != nil {
			return 0, fmt.Errorf("%s.function.arguments must be a JSON string", path)
		}
		if strings.TrimSpace(arguments) == "" || !json.Valid([]byte(arguments)) {
			return 0, fmt.Errorf("%s.function.arguments is incomplete JSON", path)
		}
	}
	return len(calls), nil
}

func rawHasValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) ||
		bytes.Equal(trimmed, []byte(`""`)) || bytes.Equal(trimmed, []byte("[]")) ||
		bytes.Equal(trimmed, []byte("{}")) || bytes.Equal(trimmed, []byte("false")) {
		return false
	}
	return true
}
