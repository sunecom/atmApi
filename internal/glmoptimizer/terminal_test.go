package glmoptimizer

import "testing"

func TestClassifyNonStreamFixtures(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		wantState  TerminalState
		wantCode   string
		consumable bool
		retryable  bool
	}{
		{
			name:       "visible content",
			fixture:    "content.json",
			wantState:  TerminalSuccessContent,
			consumable: true,
		},
		{
			name:       "complete tool call",
			fixture:    "tool_call.json",
			wantState:  TerminalSuccessToolCall,
			consumable: true,
		},
		{
			name:       "refusal",
			fixture:    "refusal.json",
			wantState:  TerminalSuccessRefusal,
			consumable: true,
		},
		{
			name:      "reasoning only",
			fixture:   "reasoning_only.json",
			wantState: TerminalFailureReasoningOnly,
			wantCode:  TerminalCodeReasoningOnly,
			retryable: true,
		},
		{
			name:      "empty choices",
			fixture:   "empty_choices.json",
			wantState: TerminalFailureEmpty,
			wantCode:  TerminalCodeEmpty,
			retryable: true,
		},
		{
			name:      "malformed json",
			fixture:   "malformed.json",
			wantState: TerminalFailureMalformed,
			wantCode:  TerminalCodeMalformed,
			retryable: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outcome := ClassifyNonStream(fixture(t, "nonstream", test.fixture))
			assertTerminalOutcome(t, outcome, test.wantState, test.wantCode, test.consumable, test.retryable)
		})
	}
}

func TestClassifyNonStreamRejectsIncompleteToolCall(t *testing.T) {
	body := []byte(`{
		"choices":[{
			"index":0,
			"message":{"role":"assistant","content":null,"tool_calls":[{
				"id":"call_broken","type":"function",
				"function":{"name":"get_weather","arguments":"{\"city\":"}
			}]},
			"finish_reason":"tool_calls"
		}]
	}`)

	outcome := ClassifyNonStream(body)
	assertTerminalOutcome(t, outcome, TerminalFailureMalformed, TerminalCodeMalformed, false, true)
	if outcome.Detail == "" {
		t.Fatal("malformed tool call must include a safe diagnostic")
	}
}

func TestClassifyNonStreamSupportsMultipleChoicesAndTools(t *testing.T) {
	body := []byte(`{
		"choices":[
			{"index":0,"message":{"role":"assistant","content":"  ","reasoning":"thinking"},"finish_reason":null},
			{"index":1,"message":{"role":"assistant","content":null,"tool_calls":[
				{"id":"call_a","type":"function","function":{"name":"first","arguments":"{}"}},
				{"id":"call_b","type":"function","function":{"name":"second","arguments":"{\"value\":2}"}}
			]},"finish_reason":"tool_calls"}
		]
	}`)

	outcome := ClassifyNonStream(body)
	assertTerminalOutcome(t, outcome, TerminalSuccessToolCall, "", true, false)
	if outcome.ChoiceIndex != 1 {
		t.Fatalf("ChoiceIndex = %d, want 1", outcome.ChoiceIndex)
	}
	if outcome.ToolCallCount != 2 {
		t.Fatalf("ToolCallCount = %d, want 2", outcome.ToolCallCount)
	}
}

func TestClassifyNonStreamPrioritizesVisibleContentAcrossChoices(t *testing.T) {
	body := []byte(`{
		"choices":[
			{"index":3,"message":{"role":"assistant","content":"","reasoning_details":[{"type":"summary","text":"thinking"}]}},
			{"index":8,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}
		]
	}`)

	outcome := ClassifyNonStream(body)
	assertTerminalOutcome(t, outcome, TerminalSuccessContent, "", true, false)
	if outcome.ChoiceIndex != 8 || outcome.FinishReason != "stop" {
		t.Fatalf("selected choice = %d/%q, want 8/stop", outcome.ChoiceIndex, outcome.FinishReason)
	}
}

func TestClassifyNonStreamDistinguishesReasoningFromCompletelyEmpty(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantState TerminalState
		wantCode  string
	}{
		{
			name:      "reasoning details only",
			body:      `{"choices":[{"index":0,"message":{"role":"assistant","content":null,"reasoning_details":[{"text":"internal"}]}}]}`,
			wantState: TerminalFailureReasoningOnly,
			wantCode:  TerminalCodeReasoningOnly,
		},
		{
			name:      "empty message",
			body:      `{"choices":[{"index":0,"message":{"role":"assistant","content":"  ","refusal":""}}]}`,
			wantState: TerminalFailureEmpty,
			wantCode:  TerminalCodeEmpty,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outcome := ClassifyNonStream([]byte(test.body))
			assertTerminalOutcome(t, outcome, test.wantState, test.wantCode, false, true)
		})
	}
}

func TestClassifyNonStreamRejectsProtocolDamagedMessages(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing message", body: `{"choices":[{"index":0,"finish_reason":"stop"}]}`},
		{name: "content has wrong type", body: `{"choices":[{"index":0,"message":{"content":{"text":"answer"}}}]}`},
		{name: "tool calls has wrong type", body: `{"choices":[{"index":0,"message":{"content":null,"tool_calls":{}}}]}`},
		{name: "missing call id", body: `{"choices":[{"index":0,"message":{"content":null,"tool_calls":[{"function":{"name":"f","arguments":"{}"}}]}}]}`},
		{name: "missing function name", body: `{"choices":[{"index":0,"message":{"content":null,"tool_calls":[{"id":"call_1","function":{"arguments":"{}"}}]}}]}`},
		{name: "arguments not string", body: `{"choices":[{"index":0,"message":{"content":null,"tool_calls":[{"id":"call_1","function":{"name":"f","arguments":{}}}]}}]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outcome := ClassifyNonStream([]byte(test.body))
			assertTerminalOutcome(t, outcome, TerminalFailureMalformed, TerminalCodeMalformed, false, true)
		})
	}
}

func assertTerminalOutcome(t *testing.T, got TerminalOutcome, state TerminalState, code string, consumable, retryable bool) {
	t.Helper()
	if got.State != state {
		t.Fatalf("State = %q, want %q (detail=%q)", got.State, state, got.Detail)
	}
	if got.ErrorCode != code {
		t.Fatalf("ErrorCode = %q, want %q", got.ErrorCode, code)
	}
	if got.Consumable != consumable {
		t.Fatalf("Consumable = %v, want %v", got.Consumable, consumable)
	}
	if got.Retryable != retryable {
		t.Fatalf("Retryable = %v, want %v", got.Retryable, retryable)
	}
}
