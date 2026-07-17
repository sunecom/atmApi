package glmoptimizer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"unicode/utf8"
)

const (
	ContextCodeInvalidRequest         = "GLM52_CONTEXT_INVALID"
	ContextCodePolicyInvalid          = "GLM52_CONTEXT_POLICY_INVALID"
	ContextCodeToolTransactionInvalid = "GLM52_TOOL_TRANSACTION_INVALID"
	ContextCodeLimitExceeded          = "GLM52_CONTEXT_LIMIT_EXCEEDED"
)

type ContextMessage map[string]json.RawMessage

type ContextPolicy struct {
	PlanName           string
	MaxInputTokens     int
	ToolOutputMaxRunes int
	ShadowTriggerRatio float64
}

type ContextDecision struct {
	PlanName                string `json:"plan_name,omitempty"`
	MaxInputTokens          int    `json:"max_input_tokens"`
	OriginalEstimatedTokens int    `json:"original_estimated_tokens"`
	FinalEstimatedTokens    int    `json:"final_estimated_tokens"`
	MessageCount            int    `json:"message_count"`
	GroupCount              int    `json:"group_count"`
	ToolTransactions        int    `json:"tool_transactions"`
	ToolMessagesCompressed  int    `json:"tool_messages_compressed"`
	ShadowGenerated         bool   `json:"shadow_generated"`
	ShadowCandidateRunes    int    `json:"shadow_candidate_runes,omitempty"`
	ShadowHashPrefix        string `json:"shadow_hash_prefix,omitempty"`
	Reason                  string `json:"reason"`
	GroupsRemoved           int    `json:"groups_removed,omitempty"` // Phase 2: MessageGroup 兜底压缩移除的组数
}

type SummaryShadow struct {
	Candidate      string `json:"-"`
	CandidateHash  string `json:"candidate_hash"`
	CandidateRunes int    `json:"candidate_runes"`
	SourceGroups   int    `json:"source_groups"`
}

type ContextError struct {
	HTTPStatus int             `json:"-"`
	Code       string          `json:"code"`
	Message    string          `json:"message"`
	Details    ContextDecision `json:"details"`
}

func (e *ContextError) Error() string { return e.Message }

type MessageGroup struct {
	Start              int  `json:"start"`
	End                int  `json:"end"`
	HasToolTransaction bool `json:"has_tool_transaction"`
}

// ApplyContextBudget performs the V0.2.1 safe transformations only: complete
// tool transactions stay intact, tool text may be deterministically reduced,
// and semantic summaries are observed in shadow mode but never sent upstream.
func ApplyContextBudget(body []byte, policy ContextPolicy) ([]byte, ContextDecision, *SummaryShadow, error) {
	decision := ContextDecision{PlanName: policy.PlanName, MaxInputTokens: policy.MaxInputTokens}
	if policy.MaxInputTokens <= 0 {
		decision.Reason = "invalid_policy"
		return nil, decision, nil, contextError(500, ContextCodePolicyInvalid,
			"GLM-5.2 套餐输入预算未正确配置", decision)
	}
	if policy.ToolOutputMaxRunes <= 0 {
		policy.ToolOutputMaxRunes = 2000
	}
	if policy.ShadowTriggerRatio <= 0 || policy.ShadowTriggerRatio >= 1 {
		policy.ShadowTriggerRatio = 0.80
	}

	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		decision.Reason = "malformed_request"
		return nil, decision, nil, contextError(400, ContextCodeInvalidRequest,
			"GLM-5.2 上下文请求格式错误", decision)
	}
	var messages []ContextMessage
	if err := json.Unmarshal(request["messages"], &messages); err != nil || messages == nil {
		decision.Reason = "invalid_messages"
		return nil, decision, nil, contextError(400, ContextCodeInvalidRequest,
			"GLM-5.2 messages 必须是数组", decision)
	}
	decision.MessageCount = len(messages)
	groups, err := GroupMessageTransactions(messages)
	if err != nil {
		decision.Reason = "invalid_tool_transaction"
		return nil, decision, nil, contextError(400, ContextCodeToolTransactionInvalid,
			"GLM-5.2 工具调用链不完整；请同时提交 assistant tool_calls 与全部对应 tool 结果", decision)
	}
	decision.GroupCount = len(groups)
	for _, group := range groups {
		if group.HasToolTransaction {
			decision.ToolTransactions++
		}
	}
	decision.OriginalEstimatedTokens = EstimateContextTokens(messages)

	updatedMessages := cloneContextMessages(messages)
	for index, message := range updatedMessages {
		if messageRole(message) != "tool" {
			continue
		}
		var content string
		if json.Unmarshal(message["content"], &content) != nil || utf8.RuneCountInString(content) <= policy.ToolOutputMaxRunes {
			continue
		}
		compressed := CompressToolContent(content, policy.ToolOutputMaxRunes)
		encoded, _ := json.Marshal(compressed)
		updatedMessages[index]["content"] = encoded
		decision.ToolMessagesCompressed++
	}
	decision.FinalEstimatedTokens = EstimateContextTokens(updatedMessages)

	var shadow *SummaryShadow
	shadowThreshold := int(float64(policy.MaxInputTokens) * policy.ShadowTriggerRatio)
	if decision.OriginalEstimatedTokens > shadowThreshold && len(groups) > 1 {
		candidate, sourceGroups := buildSummaryShadow(updatedMessages, groups)
		if candidate != "" {
			digest := sha256.Sum256([]byte(candidate))
			shadow = &SummaryShadow{
				Candidate: candidate, CandidateHash: hex.EncodeToString(digest[:]),
				CandidateRunes: utf8.RuneCountInString(candidate), SourceGroups: sourceGroups,
			}
			decision.ShadowGenerated = true
			decision.ShadowCandidateRunes = shadow.CandidateRunes
			decision.ShadowHashPrefix = shadow.CandidateHash[:12]
		}
	}

	// Phase 2: MessageGroup 级安全兜底（Dry-run 模式）
	if decision.FinalEstimatedTokens > policy.MaxInputTokens && len(groups) > 1 {
		// 尝试从最早的已完成事务开始压缩，保留 system/当前请求/最近轮次
		compressedMessages, compressedTokens, groupsRemoved := tryMessageGroupCompression(
			updatedMessages, groups, policy.MaxInputTokens)
		if groupsRemoved > 0 {
			decision.GroupsRemoved = groupsRemoved
			decision.FinalEstimatedTokens = compressedTokens
			updatedMessages = compressedMessages
			decision.Reason = "message_group_compression_applied"
			log.Printf("[GLM52上下文] MessageGroup兜底压缩: 移除%d个历史组, 估算从%d降至%d tokens",
				groupsRemoved, decision.OriginalEstimatedTokens, compressedTokens)
		}
	}

	if decision.FinalEstimatedTokens > policy.MaxInputTokens {
		decision.Reason = "safe_reduction_insufficient"
		// Phase 2: 超限错误信息优化（包含诊断信息）
		errMsg := fmt.Sprintf(
			"GLM-5.2 输入上下文约 %d tokens，超过套餐上限 %d tokens。"+
				"建议操作：1) 在客户端启用上下文压缩 2) 减少历史消息 3) 升级套餐",
			decision.FinalEstimatedTokens, policy.MaxInputTokens)
		return nil, decision, shadow, contextError(400, ContextCodeLimitExceeded, errMsg, decision)
	}
	decision.Reason = "within_budget"
	encodedMessages, _ := json.Marshal(updatedMessages)
	request["messages"] = encodedMessages
	updatedBody, err := json.Marshal(request)
	if err != nil {
		decision.Reason = "encode_failed"
		return nil, decision, shadow, contextError(500, ContextCodeInvalidRequest,
			"GLM-5.2 上下文处理失败", decision)
	}
	return updatedBody, decision, shadow, nil
}

// GroupMessageTransactions groups the leading system block and complete user
// turns. It validates that every assistant tool call has exactly one adjacent
// tool result and that no tool result is orphaned.
func GroupMessageTransactions(messages []ContextMessage) ([]MessageGroup, error) {
	if err := validateToolTransactions(messages); err != nil {
		return nil, err
	}
	groups := make([]MessageGroup, 0)
	index := 0
	for index < len(messages) && messageRole(messages[index]) == "system" {
		index++
	}
	if index > 0 {
		groups = append(groups, MessageGroup{Start: 0, End: index})
	}
	start := index
	for index < len(messages) {
		if index > start && messageRole(messages[index]) == "user" {
			groups = append(groups, messageGroup(messages, start, index))
			start = index
		}
		index++
	}
	if start < len(messages) {
		groups = append(groups, messageGroup(messages, start, len(messages)))
	}
	return groups, nil
}

func validateToolTransactions(messages []ContextMessage) error {
	pending := map[string]bool{}
	for index, message := range messages {
		role := messageRole(message)
		if len(pending) > 0 {
			if role != "tool" {
				return fmt.Errorf("tool transaction before message %d is incomplete", index)
			}
			var callID string
			if json.Unmarshal(message["tool_call_id"], &callID) != nil || !pending[callID] {
				return fmt.Errorf("tool message %d has unknown tool_call_id", index)
			}
			delete(pending, callID)
			continue
		}
		if role == "tool" {
			return fmt.Errorf("tool message %d is orphaned", index)
		}
		if role != "assistant" {
			continue
		}
		raw, found := message["tool_calls"]
		if !found || string(raw) == "null" {
			continue
		}
		var calls []struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &calls) != nil {
			return fmt.Errorf("assistant message %d has invalid tool_calls", index)
		}
		if len(calls) == 0 {
			continue
		}
		for _, call := range calls {
			if strings.TrimSpace(call.ID) == "" || pending[call.ID] {
				return fmt.Errorf("assistant message %d has invalid tool call id", index)
			}
			pending[call.ID] = true
		}
	}
	if len(pending) > 0 {
		return fmt.Errorf("tool transaction is incomplete")
	}
	return nil
}

func messageGroup(messages []ContextMessage, start, end int) MessageGroup {
	group := MessageGroup{Start: start, End: end}
	for _, message := range messages[start:end] {
		if _, found := message["tool_calls"]; found {
			group.HasToolTransaction = true
			break
		}
	}
	return group
}

func messageRole(message ContextMessage) string {
	var role string
	_ = json.Unmarshal(message["role"], &role)
	return role
}

func cloneContextMessages(messages []ContextMessage) []ContextMessage {
	result := make([]ContextMessage, len(messages))
	for index, message := range messages {
		result[index] = make(ContextMessage, len(message))
		for key, value := range message {
			result[index][key] = append(json.RawMessage(nil), value...)
		}
	}
	return result
}

// EstimateContextTokens is deliberately conservative for mixed Chinese and
// code: non-ASCII runes count as one token, ASCII bytes as one quarter token,
// plus a small per-message protocol overhead.
func EstimateContextTokens(messages []ContextMessage) int {
	encoded, _ := json.Marshal(messages)
	ascii, nonASCII := 0, 0
	for _, r := range string(encoded) {
		if r <= 0x7f {
			ascii++
		} else {
			nonASCII++
		}
	}
	return (ascii+3)/4 + nonASCII + len(messages)*4
}

// CompressToolContent creates deterministic, rune-safe evidence. JSON tool
// output stays valid JSON by using a small compression envelope.
func CompressToolContent(content string, maxRunes int) string {
	if maxRunes < 256 {
		maxRunes = 256
	}
	if utf8.RuneCountInString(content) <= maxRunes {
		return content
	}
	lines := collapseConsecutiveLines(strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n"))
	head := selectLines(lines, 0, 6)
	tailStart := len(lines) - 10
	if tailStart < 0 {
		tailStart = 0
	}
	tail := selectLines(lines, tailStart, len(lines))
	evidence := collectEvidence(lines, 12)
	digest := sha256.Sum256([]byte(content))
	if json.Valid([]byte(content)) {
		envelope := map[string]interface{}{
			"_atmapi_compressed": true,
			"original_sha256":    hex.EncodeToString(digest[:]),
			"original_runes":     utf8.RuneCountInString(content),
			"evidence":           evidence, "head": head, "tail": tail,
		}
		for {
			encoded, _ := json.Marshal(envelope)
			if utf8.RuneCount(encoded) <= maxRunes || (len(head) == 0 && len(tail) == 0 && len(evidence) == 0) {
				return string(encoded)
			}
			if len(head) > 0 {
				head = head[:len(head)-1]
				envelope["head"] = head
				continue
			}
			if len(tail) > 0 {
				tail = tail[1:]
				envelope["tail"] = tail
				continue
			}
			evidence = evidence[:len(evidence)-1]
			envelope["evidence"] = evidence
		}
	}
	parts := []string{
		fmt.Sprintf("[atmApi tool output compressed: original_runes=%d sha256=%s]", utf8.RuneCountInString(content), hex.EncodeToString(digest[:6])),
	}
	if len(evidence) > 0 {
		parts = append(parts, "[evidence]", strings.Join(evidence, "\n"))
	}
	if len(head) > 0 {
		parts = append(parts, "[head]", strings.Join(head, "\n"))
	}
	if len(tail) > 0 {
		parts = append(parts, "[tail]", strings.Join(tail, "\n"))
	}
	return truncateRunes(strings.Join(parts, "\n"), maxRunes)
}

func collapseConsecutiveLines(lines []string) []string {
	result := make([]string, 0, len(lines))
	for index := 0; index < len(lines); {
		next := index + 1
		for next < len(lines) && lines[next] == lines[index] {
			next++
		}
		result = append(result, truncateRunes(lines[index], 160))
		if count := next - index; count > 1 {
			result = append(result, fmt.Sprintf("[previous line repeated %d times]", count-1))
		}
		index = next
	}
	return result
}

func collectEvidence(lines []string, limit int) []string {
	result := make([]string, 0, limit)
	seen := map[string]bool{}
	for _, line := range lines {
		lower := strings.ToLower(line)
		important := strings.Contains(lower, "error") || strings.Contains(lower, "fail") ||
			strings.Contains(lower, "exception") || strings.Contains(lower, "panic") ||
			strings.Contains(lower, "exit code") || strings.Contains(lower, "exit=") ||
			strings.Contains(lower, "返回码") || strings.Contains(line, "/")
		line = truncateRunes(line, 160)
		if important && !seen[line] {
			seen[line] = true
			result = append(result, line)
			if len(result) == limit {
				break
			}
		}
	}
	return result
}

func selectLines(lines []string, start, end int) []string {
	if start < 0 {
		start = 0
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		start = end
	}
	result := make([]string, 0, end-start)
	for _, line := range lines[start:end] {
		result = append(result, truncateRunes(line, 160))
	}
	return result
}

func truncateRunes(value string, maximum int) string {
	if maximum <= 0 || utf8.RuneCountInString(value) <= maximum {
		return value
	}
	runes := []rune(value)
	if maximum <= 1 {
		return string(runes[:maximum])
	}
	return string(runes[:maximum-1]) + "…"
}

func buildSummaryShadow(messages []ContextMessage, groups []MessageGroup) (string, int) {
	if len(groups) <= 1 {
		return "", 0
	}
	parts := make([]string, 0)
	for _, group := range groups[:len(groups)-1] {
		for _, message := range messages[group.Start:group.End] {
			var content string
			if json.Unmarshal(message["content"], &content) != nil || strings.TrimSpace(content) == "" {
				continue
			}
			parts = append(parts, messageRole(message)+": "+truncateRunes(strings.TrimSpace(content), 180))
		}
	}
	return truncateRunes(strings.Join(parts, "\n"), 1200), len(groups) - 1
}

// tryMessageGroupCompression 尝试从最早的已完成事务开始压缩，保留 system/当前请求/最近轮次
// 返回：压缩后的 messages、估算的 tokens、移除的 group 数量
func tryMessageGroupCompression(messages []ContextMessage, groups []MessageGroup, maxTokens int) ([]ContextMessage, int, int) {
	if len(groups) <= 1 {
		return messages, EstimateContextTokens(messages), 0
	}

	// 保留最后一个 group（当前请求），尝试移除前面的 group
	groupsToRemove := len(groups) - 1

	// 从最早的 group 开始，逐步移除，直到低于 maxTokens
	for removeCount := 1; removeCount <= groupsToRemove; removeCount++ {
		// 保留从 groups[removeCount].Start 到最后的 messages
		keepStart := groups[removeCount].Start
		if keepStart >= len(messages) {
			break
		}

		// 构建保留的 messages（保留 system + 从 keepStart 开始的所有内容）
		var keptMessages []ContextMessage
		
		// 先检查是否有 system 消息
		for i := 0; i < keepStart; i++ {
			if messageRole(messages[i]) == "system" {
				keptMessages = append(keptMessages, messages[i])
				break // 只保留第一个 system 消息
			}
		}
		
		// 保留从 keepStart 开始的所有 messages
		keptMessages = append(keptMessages, messages[keepStart:]...)

		// 估算 tokens
		tokens := EstimateContextTokens(keptMessages)
		
		log.Printf("[GLM52上下文] MessageGroup压缩尝试: 移除前%d个group, 保留%d条消息, 估算%d tokens (上限%d)",
			removeCount, len(keptMessages), tokens, maxTokens)

		if tokens <= maxTokens {
			log.Printf("[GLM52上下文] MessageGroup压缩成功: 从%d tokens降至%d tokens, 移除%d个历史group",
				EstimateContextTokens(messages), tokens, removeCount)
			return keptMessages, tokens, removeCount
		}
	}

	// 即使移除所有历史 group 仍然超限，返回当前状态
	log.Printf("[GLM52上下文] MessageGroup压缩失败: 即使移除所有历史group仍超限")
	return messages, EstimateContextTokens(messages), 0
}

func contextError(status int, code, message string, decision ContextDecision) *ContextError {
	return &ContextError{HTTPStatus: status, Code: code, Message: message, Details: decision}
}
