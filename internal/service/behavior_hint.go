package service

import (
	"fmt"
	"log"
	"regexp"
	"strings"

	"atmapi/internal/glmoptimizer"
)

// ObserveGLM52SummaryShadow records only safe comparison metadata. Candidate
// summary text is deliberately excluded from logs because it may contain
// prompt content; shadow mode never injects the candidate into upstream input.
func ObserveGLM52SummaryShadow(decision glmoptimizer.ContextDecision, shadow *glmoptimizer.SummaryShadow) {
	if shadow == nil {
		return
	}
	log.Printf("[GLM52摘要shadow] plan=%q original_tokens=%d final_tokens=%d groups=%d source_groups=%d candidate_runes=%d candidate_hash=%s",
		decision.PlanName, decision.OriginalEstimatedTokens, decision.FinalEstimatedTokens,
		decision.GroupCount, shadow.SourceGroups, shadow.CandidateRunes, decision.ShadowHashPrefix)
}

// ===== 行为修正引擎（Phase 2） =====
// 检测 AI 对话中的低效模式，主动注入 system hint 减少冗余轮次
// 三档检测：
//   1. confirmation_loop — 连续 >3 次"可以吗/继续吗"式确认 → 注入"一次性给完整方案"
//   2. fragmented_output — 连续 >5 次短轮（<50 字）→ 注入"请完整回复"
//   3. verbose_context — 估算 tokens >50K → 注入"直接给最终方案，不逐项确认"
//
// 设计原则（第一性原理）：
//   - 不替用户做决定，只帮 AI 减少不必要的 token 浪费
//   - 不改变对话内容质量
//   - 仅消除冗余确认轮次和碎片化输出

// BehaviorHint 检测到的行为修正建议
type BehaviorHint struct {
	Pattern  string // "verbose" | "confirmation_loop" | "fragmented"
	Hint     string // 要注入的系统提示
	Priority int    // 优先级（高优先级覆盖低优先级）
}

// 确认模式关键词正则（匹配 AI 回复中的确认请求）
// 例如："这样可以吗？" "需要我继续吗？" "你觉得呢？"
var confirmationPattern = regexp.MustCompile(`(?i)(可以吗|继续吗|需要我|你觉得|行不行|对不对|是否|要不要|确认一下|可以继续)`)

// 短轮阈值（字符数）
const (
	ShortReplyThreshold   = 80    // <80 字算短轮
	FragmentedCountMax    = 5     // >5 次短轮触发
	ConfirmationCountMax  = 3     // >3 次确认触发
	VerboseTokenThreshold = 50000 // >50K tokens 触发
)

// DetectAndFixBehavior 检测对话模式，返回需要注入的 hint
// 在 routes.go 中调用：messages = CompressContext(messages, tokenKey) 之后
// 然后 DetectAndFixBehavior(messages)，如果有 hint 则追加到 messages
func DetectAndFixBehavior(messages []map[string]interface{}, estTokens int) *BehaviorHint {
	if len(messages) < 4 {
		return nil // 对话太短，不检测
	}

	// 收集 assistant 回复
	var assistantReplies []string
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		if role != "assistant" {
			continue
		}
		text := getUserText(msg)
		if text != "" {
			assistantReplies = append(assistantReplies, text)
		}
	}

	if len(assistantReplies) < 3 {
		return nil // 回复太少，不检测
	}

	hints := make([]*BehaviorHint, 0)

	// ===== 检测 1：confirmation_loop =====
	confCount := 0
	for _, reply := range assistantReplies {
		if confirmationPattern.MatchString(reply) {
			confCount++
		}
	}
	if confCount > ConfirmationCountMax {
		hints = append(hints, &BehaviorHint{
			Pattern:  "confirmation_loop",
			Hint:     "当前上下文成本较高。除非缺少阻塞信息，否则请直接完成任务；减少确认式提问；压缩中间解释；最终给出结果、验证和风险。",
			Priority: 10,
		})
		log.Printf("[行为修正] confirmation_loop: %d 次确认请求", confCount)
	}

	// ===== 检测 2：fragmented_output =====
	// 只看最近的回复（最后 10 条 assistant 消息）
	recentReplies := assistantReplies
	if len(recentReplies) > 10 {
		recentReplies = recentReplies[len(recentReplies)-10:]
	}

	shortCount := 0
	for _, reply := range recentReplies {
		if len(reply) < ShortReplyThreshold {
			shortCount++
		}
	}
	if shortCount > FragmentedCountMax {
		hints = append(hints, &BehaviorHint{
			Pattern:  "fragmented",
			Hint:     "当前上下文成本较高。除非缺少阻塞信息，否则请直接完成任务；减少确认式提问；压缩中间解释；最终给出结果、验证和风险。",
			Priority: 8,
		})
		log.Printf("[行为修正] fragmented_output: %d/%d 次短轮", shortCount, len(recentReplies))
	}

	// ===== 检测 3：verbose_context =====
	if estTokens > VerboseTokenThreshold {
		hints = append(hints, &BehaviorHint{
			Pattern:  "verbose",
			Hint:     "注意：当前对话上下文较长。请直接给出最终方案和结论，不需要重复已知背景，不需要逐项确认。保持回复简洁高效。",
			Priority: 5,
		})
		log.Printf("[行为修正] verbose_context: estTokens=%d", estTokens)
	}

	if len(hints) == 0 {
		return nil
	}

	// 取最高优先级的 hint
	best := hints[0]
	for _, h := range hints[1:] {
		if h.Priority > best.Priority {
			best = h
		}
	}

	log.Printf("[行为修正] 选中: pattern=%s priority=%d", best.Pattern, best.Priority)
	return best
}

// ApplyBehaviorHint 将 hint 注入到 messages 中（追加为 system 消息）
// 使用 InsertAfterSystemBlock 保证前缀稳定
func ApplyBehaviorHint(messages []map[string]interface{}, hint *BehaviorHint) []map[string]interface{} {
	if hint == nil {
		return messages
	}

	hintMsg := map[string]interface{}{
		"role":    "system",
		"content": hint.Hint,
	}

	result := InsertAfterSystemBlock(messages, hintMsg)

	// 计算插入位置用于日志
	insertAt := 0
	for i, m := range messages {
		role, _ := m["role"].(string)
		if role == "system" {
			insertAt = i + 1
		} else {
			break
		}
	}
	log.Printf("[行为修正] 已注入 hint: pattern=%s at position=%d", hint.Pattern, insertAt)
	return result
}

// ShouldApplyBehaviorHint 根据套餐决定是否启用行为修正
// Phase 3 预留：基础版强制启用，旗舰版可选关闭
func ShouldApplyBehaviorHint(tokenKey string) bool {
	// Phase 2：所有套餐默认启用
	// Phase 3 可以根据 plan 配置开关
	return true
}

// EstimateTokensForBehavior 供 routes.go 调用的便捷方法
// 如果已经算过 estTokens 直接传入，否则现场算
func EstimateTokensForBehavior(messages []map[string]interface{}) int {
	return estimateTokens(messages)
}

// FormatBehaviorStats 格式化行为检测统计（用于日志/调试）
func FormatBehaviorStats(messages []map[string]interface{}) string {
	var assistantCount, userCount, systemCount int
	var totalReplyLen int
	var shortReplies int

	for _, msg := range messages {
		role, _ := msg["role"].(string)
		text := getUserText(msg)
		switch role {
		case "assistant":
			assistantCount++
			totalReplyLen += len(text)
			if len(text) < ShortReplyThreshold {
				shortReplies++
			}
		case "user":
			userCount++
		case "system":
			systemCount++
		}
	}

	avgReplyLen := 0
	if assistantCount > 0 {
		avgReplyLen = totalReplyLen / assistantCount
	}

	return fmt.Sprintf("msg=%d (sys=%d usr=%d ast=%d) shortReplies=%d avgReplyLen=%d",
		len(messages), systemCount, userCount, assistantCount, shortReplies, avgReplyLen)
}

// ===== Prompt Cache 前缀稳定化（Phase 2B） =====
// 所有 system message 注入都应使用 InsertAfterSystemBlock
// 这样 messages[0] 永远是客户端原始 system prompt，上游模型可稳定缓存前缀

// InsertAfterSystemBlock 将一条 system message 插入到 messages 中连续 system block 的末尾
// 如果 messages 开头没有 system，则 prepend
// 这保证了 messages[0] 的稳定性，让上游模型（DeepSeek/OpenAI/Anthropic）的 prompt cache 能命中
func InsertAfterSystemBlock(messages []map[string]interface{}, msg map[string]interface{}) []map[string]interface{} {
	insertAt := 0
	for i, m := range messages {
		role, _ := m["role"].(string)
		if role == "system" {
			insertAt = i + 1
		} else {
			break
		}
	}

	result := make([]map[string]interface{}, 0, len(messages)+1)
	if insertAt == 0 {
		result = append(result, msg)
		result = append(result, messages...)
	} else {
		result = append(result, messages[:insertAt]...)
		result = append(result, msg)
		result = append(result, messages[insertAt:]...)
	}
	return result
}

// ===== 工具输出压缩（Phase 2A+ 第二道防线） =====
// 压缩 role=tool 的消息，减少 token 大户

// CompressToolOutput 压缩工具输出
// 策略：保留最后 50 行 + 错误信息 + 文件路径 + 退出码
func CompressToolOutput(messages []map[string]interface{}) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(messages))
	compressedCount := 0

	for _, msg := range messages {
		role, _ := msg["role"].(string)
		if role != "tool" {
			result = append(result, msg)
			continue
		}

		content := getUserText(msg)
		if content == "" || len(content) <= 2000 {
			result = append(result, msg)
			continue
		}

		// 压缩
		compressed := compressToolOutputContent(content)
		newMsg := make(map[string]interface{})
		for k, v := range msg {
			newMsg[k] = v
		}
		newMsg["content"] = compressed
		result = append(result, newMsg)
		compressedCount++
	}

	if compressedCount > 0 {
		log.Printf("[工具压缩] 压缩了 %d 条工具输出", compressedCount)
	}
	return result
}

// compressToolOutputContent 压缩单条工具输出内容
func compressToolOutputContent(content string) string {
	lines := strings.Split(content, "\n")

	// 1. 提取关键信息
	var keyInfo []string

	// 错误信息（包含 error/fail/exception）
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") ||
			strings.Contains(lower, "fail") ||
			strings.Contains(lower, "exception") {
			keyInfo = append(keyInfo, line)
		}
	}

	// 文件路径（/path/to/file 格式）
	pathRegex := regexp.MustCompile(`/[\w./\-]+`)
	paths := pathRegex.FindAllString(content, -1)
	if len(paths) > 0 {
		uniquePaths := unique(paths)
		if len(uniquePaths) > 5 {
			uniquePaths = uniquePaths[:5] // 最多 5 个路径
		}
		keyInfo = append(keyInfo, "文件: "+strings.Join(uniquePaths, ", "))
	}

	// 退出码（exit code: X 或 exit=X）
	exitRegex := regexp.MustCompile(`(?i)(exit\s*(code)?[=:]\s*(\d+)|返回码[=:]\s*(\d+))`)
	if matches := exitRegex.FindStringSubmatch(content); len(matches) > 1 {
		for _, m := range matches[1:] {
			if m != "" {
				keyInfo = append(keyInfo, "退出码: "+m)
				break
			}
		}
	}

	// 2. 保留最后 50 行
	if len(lines) > 50 {
		lines = lines[len(lines)-50:]
	}

	// 3. 组合
	result := strings.Join(keyInfo, "\n")
	if result != "" {
		result += "\n\n--- 最后 50 行 ---\n"
	}
	result += strings.Join(lines, "\n")

	// 4. 截断到 2000 字
	if len(result) > 2000 {
		result = result[:2000] + "\n... [已截断]"
	}

	return result
}

// unique 去重字符串切片
func unique(strs []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(strs))
	for _, s := range strs {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// stripMarkdown 简单去除 markdown 标记，用于更准确的长度判断
func stripMarkdown(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "`", "")
	s = strings.ReplaceAll(s, "#", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "*", "")
	return s
}

// ===== 默认行为策略（Phase 2A+ 第一道防线） =====
// 对开发类任务，从源头注入默认策略，减少确认轮次

// 开发类关键词
var devKeywords = []string{
	"写", "实现", "修复", "开发", "代码", "函数", "bug", "调试",
	"编写", "重构", "优化", "部署", "配置", "安装", "搭建",
}

// 咨询类关键词（排除）
var consultKeywords = []string{
	"解释", "什么是", "为什么", "帮我理解", "原理", "概念",
	"区别", "差异", "对比", "介绍", "说明",
}

// IsDevelopmentTask 判断最后一条用户消息是否为开发类任务
func IsDevelopmentTask(messages []map[string]interface{}) bool {
	// 从后往前找最后一条 user 消息
	for i := len(messages) - 1; i >= 0; i-- {
		role, _ := messages[i]["role"].(string)
		if role != "user" {
			continue
		}
		text := getUserText(messages[i])
		if text == "" {
			continue
		}

		// 检查是否命中咨询类（优先排除）
		for _, kw := range consultKeywords {
			if strings.Contains(text, kw) {
				return false
			}
		}

		// 检查是否命中开发类
		for _, kw := range devKeywords {
			if strings.Contains(text, kw) {
				return true
			}
		}

		// 都没命中，不算开发类
		return false
	}
	return false
}

// DefaultBehaviorHint 默认行为策略 hint
const DefaultBehaviorHint = "能直接做就直接做；只有遇到阻塞性信息缺失才提问；中间状态简短汇报；最终一次性总结结果。"

// ApplyDefaultBehaviorStrategy 对开发类任务注入默认行为策略
// 使用 InsertAfterSystemBlock 保证前缀稳定
// 安全补丁：如果用户明确要求分步确认，跳过注入
func ApplyDefaultBehaviorStrategy(messages []map[string]interface{}) ([]map[string]interface{}, bool) {
	if !IsDevelopmentTask(messages) {
		return messages, false
	}

	// 安全补丁：用户要求分步确认时，不注入默认策略
	if UserWantsStepByStep(messages) {
		log.Printf("[默认策略] 用户要求分步确认，跳过默认策略注入")
		return messages, false
	}

	hintMsg := map[string]interface{}{
		"role":    "system",
		"content": DefaultBehaviorHint,
	}

	result := InsertAfterSystemBlock(messages, hintMsg)
	log.Printf("[默认策略] 开发类任务，注入默认行为策略")
	return result, true
}

// ===== 安全补丁：用户分步意图检测（Phase 2C） =====
// 当用户明确要求"分步骤/一步步/先问我确认"时，跳过所有行为修正
// 防止"过度修正"——用户要求分步确认时强行"一次性完成"会降低质量

// stepByStepPattern 匹配用户要求分步确认的意图
var stepByStepPattern = regexp.MustCompile(`(?i)(分步[骤来]?|一步步|一步一步|逐步|先问我|先确认|一步一步来|慢慢来|不要急|分步确认|逐步确认|每[一每]步|等我确认|我确认[后再之]|需要我确认|先给[我你]看|先[不别]急)`)

// UserWantsStepByStep 检测用户是否要求分步确认
// 检查最后一条用户消息，如果包含分步意图关键词则返回 true
func UserWantsStepByStep(messages []map[string]interface{}) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		role, _ := messages[i]["role"].(string)
		if role != "user" {
			continue
		}
		text := getUserText(messages[i])
		if text == "" {
			continue
		}
		if stepByStepPattern.MatchString(text) {
			log.Printf("[安全补丁] 检测到用户分步意图: %s", truncateForLog(text, 50))
			return true
		}
		// 只检查最后一条 user 消息
		return false
	}
	return false
}

// ShouldSkipBehaviorHint 综合判断是否应跳过行为修正
// 在 routes.go 中调用，替代直接调用 ShouldApplyBehaviorHint
func ShouldSkipBehaviorHint(messages []map[string]interface{}, tokenKey string) bool {
	// 用户要求分步确认 → 跳过
	if UserWantsStepByStep(messages) {
		return true
	}
	// 套餐级别开关
	if !ShouldApplyBehaviorHint(tokenKey) {
		return true
	}
	return false
}

// truncateForLog 截断字符串用于日志
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
