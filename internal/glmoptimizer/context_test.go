package glmoptimizer

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestApplyContextBudgetPreservesCompleteToolTransaction(t *testing.T) {
	request, _ := json.Marshal(map[string]interface{}{
		"model": "glm-5.2",
		"messages": []map[string]interface{}{
			{"role": "system", "content": "stable"},
			{"role": "user", "content": "run both"},
			{"role": "assistant", "content": nil, "tool_calls": []map[string]interface{}{
				{"id": "call-a", "type": "function", "function": map[string]interface{}{"name": "a", "arguments": "{}"}},
				{"id": "call-b", "type": "function", "function": map[string]interface{}{"name": "b", "arguments": "{}"}},
			}},
			{"role": "tool", "tool_call_id": "call-a", "content": strings.Repeat("line a\n", 800)},
			{"role": "tool", "tool_call_id": "call-b", "content": strings.Repeat("line b\n", 800)},
			{"role": "assistant", "content": "done"},
			{"role": "user", "content": "next turn"},
		},
	})
	updated, decision, _, err := ApplyContextBudget(request, ContextPolicy{MaxInputTokens: 5000, ToolOutputMaxRunes: 600})
	if err != nil {
		t.Fatal(err)
	}
	if decision.ToolMessagesCompressed != 2 {
		t.Fatalf("compressed=%d, want 2", decision.ToolMessagesCompressed)
	}
	var envelope struct {
		Messages []map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(updated, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Messages) != 7 {
		t.Fatalf("message count changed: %d", len(envelope.Messages))
	}
	roles := make([]string, len(envelope.Messages))
	for index, message := range envelope.Messages {
		_ = json.Unmarshal(message["role"], &roles[index])
	}
	if strings.Join(roles, ",") != "system,user,assistant,tool,tool,assistant,user" {
		t.Fatalf("transaction order changed: %v", roles)
	}
	for index, wantID := range []string{"call-a", "call-b"} {
		var gotID string
		_ = json.Unmarshal(envelope.Messages[index+3]["tool_call_id"], &gotID)
		if gotID != wantID {
			t.Fatalf("tool id[%d]=%q, want %q", index, gotID, wantID)
		}
	}
}

func TestGroupMessageTransactionsNeverSplitsToolChain(t *testing.T) {
	messages := decodeContextMessages(t, []byte(`[
 {"role":"system","content":"s"},
 {"role":"user","content":"u1"},
 {"role":"assistant","tool_calls":[{"id":"x","function":{"name":"f","arguments":"{}"}}]},
 {"role":"tool","tool_call_id":"x","content":"result"},
 {"role":"assistant","content":"answer"},
 {"role":"user","content":"u2"}
]`))
	groups, err := GroupMessageTransactions(messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 3 || groups[1].Start != 1 || groups[1].End != 5 || !groups[1].HasToolTransaction {
		t.Fatalf("unexpected groups: %+v", groups)
	}
}

func TestApplyContextBudgetRejectsIncompleteToolTransaction(t *testing.T) {
	request := []byte(`{"model":"glm-5.2","messages":[
 {"role":"user","content":"run"},
 {"role":"assistant","tool_calls":[{"id":"missing","function":{"name":"f","arguments":"{}"}}]}
]}`)
	_, _, _, err := ApplyContextBudget(request, ContextPolicy{MaxInputTokens: 1000})
	contextErr, ok := err.(*ContextError)
	if !ok || contextErr.Code != ContextCodeToolTransactionInvalid {
		t.Fatalf("error=%T %+v", err, err)
	}
}

func TestDeterministicToolCompressionPreservesUTF8AndJSON(t *testing.T) {
	text := strings.Repeat("中文🙂 duplicate line\n", 400) + "ERROR: 文件 /tmp/测试.json\nexit code: 7"
	first := CompressToolContent(text, 500)
	second := CompressToolContent(text, 500)
	if first != second || !utf8.ValidString(first) {
		t.Fatalf("compression is not deterministic UTF-8: equal=%t valid=%t", first == second, utf8.ValidString(first))
	}
	if !strings.Contains(first, "ERROR") || !strings.Contains(first, "exit code: 7") {
		t.Fatalf("critical evidence missing: %s", first)
	}

	jsonTool := `{"状态":"成功🙂","items":[` + strings.Repeat(`{"路径":"/tmp/文件"},`, 200) + `null]}`
	compressedJSON := CompressToolContent(jsonTool, 500)
	if !utf8.ValidString(compressedJSON) || !json.Valid([]byte(compressedJSON)) {
		t.Fatalf("compressed JSON is invalid: %s", compressedJSON)
	}
}

func TestSummaryShadowDoesNotReplaceUpstreamMessages(t *testing.T) {
	request := []byte(`{"model":"glm-5.2","messages":[
 {"role":"system","content":"policy"},
 {"role":"user","content":"` + strings.Repeat("历史事实甲🙂", 200) + `"},
 {"role":"assistant","content":"` + strings.Repeat("历史结论乙", 200) + `"},
 {"role":"user","content":"current request"}
]}`)
	updated, decision, shadow, err := ApplyContextBudget(request, ContextPolicy{MaxInputTokens: 3000, ShadowTriggerRatio: 0.2})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.ShadowGenerated || shadow == nil || shadow.Candidate == "" || shadow.CandidateHash == "" {
		t.Fatalf("shadow not generated: decision=%+v shadow=%+v", decision, shadow)
	}
	canonicalOriginal, _ := CanonicalizeJSON(request)
	canonicalUpdated, _ := CanonicalizeJSON(updated)
	if !bytes.Equal(canonicalOriginal, canonicalUpdated) {
		t.Fatalf("shadow mode mutated upstream messages:\n%s\n%s", canonicalOriginal, canonicalUpdated)
	}
}

func TestApplyContextBudgetReturnsExplicitLimitError(t *testing.T) {
	request := []byte(`{"model":"glm-5.2","messages":[{"role":"user","content":"` + strings.Repeat("不可删除的用户上下文🙂", 1000) + `"}]}`)
	_, decision, _, err := ApplyContextBudget(request, ContextPolicy{MaxInputTokens: 100, ToolOutputMaxRunes: 200})
	contextErr, ok := err.(*ContextError)
	if !ok || contextErr.Code != ContextCodeLimitExceeded || contextErr.HTTPStatus != 400 {
		t.Fatalf("error=%T %+v", err, err)
	}
	if decision.FinalEstimatedTokens <= decision.MaxInputTokens {
		t.Fatalf("unexpected decision: %+v", decision)
	}
	if strings.Contains(contextErr.Error(), "不可删除") {
		t.Fatal("context error leaked prompt content")
	}
}

func decodeContextMessages(t *testing.T, body []byte) []ContextMessage {
	t.Helper()
	var messages []ContextMessage
	if err := json.Unmarshal(body, &messages); err != nil {
		t.Fatal(err)
	}
	return messages
}
