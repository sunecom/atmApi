package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"atmapi/internal/glmoptimizer"
	"atmapi/internal/model"
)

// ===== 上下文压缩引擎 v3（成本感知） =====
// 两级策略，阈值根据套餐 MaxInputTokens 按比例计算：
//   > 50% MaxInputTokens → 历史裁剪
//   > 80% MaxInputTokens → 摘要替换
//   MaxInputTokens=0（不限量）→ 回退默认硬编码阈值

const (
	ThresholdTruncate = 30000 // 默认截断阈值（无套餐或无限量时回退）
	ThresholdSummary  = 60000 // 默认摘要阈值（无套餐或无限量时回退）
	TruncateRatio     = 0.50  // MaxInputTokens 的 50% 触发截断
	SummaryRatio      = 0.80  // MaxInputTokens 的 80% 触发摘要
	MinTruncateEst    = 5000  // estTokens 安全下限（防误触发）
	TailKeepMessages  = 6     // 保留最后 6 条（约 3 轮对话）
	SummaryMaxTokens  = 800   // 摘要生成 max_tokens
	SummaryTimeout    = 30    // 摘要请求超时秒数
)

// PrepareGLM52Context is the only context-processing entry used by the
// GLM-5.2 path. It never calls the legacy DeepSeek Flash summarizer and never
// replaces history with a semantic summary.
//
// Phase 0D: Threshold alignment with OpenClaw compact strategy
// - OpenClaw compacts at 70-80% of context window
// - atmApi acts as safety net at higher thresholds
// - Shadow observation triggers at 90% (only for monitoring, not action)
// - Hard rejection only as last resort when over 100%
func PrepareGLM52Context(body []byte, planName string, maxInputTokens int) ([]byte, glmoptimizer.ContextDecision, error) {
	updated, decision, shadow, err := glmoptimizer.ApplyContextBudget(body, glmoptimizer.ContextPolicy{
		PlanName: planName, MaxInputTokens: maxInputTokens,
		ToolOutputMaxRunes: 2000, ShadowTriggerRatio: 0.90,
	})
	ObserveGLM52SummaryShadow(decision, shadow)
	return updated, decision, err
}

// CompressContext 上下文压缩入口（成本感知 v3）
// 根据套餐 MaxInputTokens 按比例计算阈值，更高配的套餐推迟压缩
func CompressContext(messages []map[string]interface{}, tokenKey string) []map[string]interface{} {
	estTokens := estimateTokens(messages)

	// ===== 成本感知阈值计算 =====
	truncateThreshold := ThresholdTruncate
	summaryThreshold := ThresholdSummary

	if plan := lookupPlanForToken(tokenKey); plan != nil && plan.MaxInputTokens > 0 {
		pTruncate := int(float64(plan.MaxInputTokens) * TruncateRatio)
		pSummary := int(float64(plan.MaxInputTokens) * SummaryRatio)

		// 防止阈值过低导致误触发
		if pTruncate < MinTruncateEst {
			pTruncate = MinTruncateEst
		}
		if pSummary <= pTruncate {
			pSummary = pTruncate + MinTruncateEst
		}

		truncateThreshold = pTruncate
		summaryThreshold = pSummary
		log.Printf("[压缩] 成本感知: plan=%s MaxInputTokens=%d → truncate@%d summary@%d (est=%d)",
			plan.Name, plan.MaxInputTokens, truncateThreshold, summaryThreshold, estTokens)
	} else {
		log.Printf("[压缩] 默认阈值: truncate@%d summary@%d (est=%d)", truncateThreshold, summaryThreshold, estTokens)
	}

	if estTokens <= truncateThreshold {
		log.Printf("[压缩] 无需压缩: est=%d ≤ truncate=%d", estTokens, truncateThreshold)
		return messages
	}

	systemMsgs, middleMsgs, tailMsgs := splitMessagesSafe(messages, TailKeepMessages)

	log.Printf("[压缩] estTokens=%d system=%d middle=%d tail=%d",
		estTokens, len(systemMsgs), len(middleMsgs), len(tailMsgs))

	if len(middleMsgs) == 0 {
		return messages
	}

	// > summaryThreshold → 摘要替换
	if estTokens > summaryThreshold {
		summary := generateSummary(middleMsgs)
		if summary != "" {
			result := mergeWithSummary(systemMsgs, summary, tailMsgs, len(middleMsgs))
			log.Printf("[压缩] ✓ 摘要替换完成: %d 条中间消息 → 1 条摘要", len(middleMsgs))
			return result
		}
		log.Printf("[压缩] 摘要生成失败，降级为历史裁剪")
	}

	// > truncateThreshold → 历史裁剪（会删除中间历史，并非无损）
	result := mergeTruncated(systemMsgs, tailMsgs, len(middleMsgs))
	log.Printf("[压缩] ✓ 历史裁剪完成: 丢弃 %d 条中间消息", len(middleMsgs))
	return result
}

// estimateTokens 粗估 token 数
// chars/3 对中文偏小（实测 1.72×：chars/3=77K，DeepSeek=133K）
// 原因是 len() 对中文返回 bytes（3B/字），但 chars/3 实际是 bytes/3
// 中文约 0.5 字/token，但 DeepSeek 编码复杂（含元数据、JSON、空白等）
// 改进：用 bytes/2 × 1.1 更接近实际 token 计数
func estimateTokens(messages []map[string]interface{}) int {
	var totalBytes int
	for _, msg := range messages {
		switch c := msg["content"].(type) {
		case string:
			totalBytes += len(c)
		case []interface{}:
			for _, part := range c {
				if pm, ok := part.(map[string]interface{}); ok {
					if t, ok := pm["text"].(string); ok {
						totalBytes += len(t)
					}
					if typ, _ := pm["type"].(string); typ == "image_url" || typ == "image" {
						totalBytes += 3000 // 图片每张 ~1K tokens
					}
				}
			}
		}
	}
	// bytes/2 × 1.05 ≈ tokens（校准系数，混合中英文场景）
	return int(float64(totalBytes) / 2.0 * 1.05)
}

// lookupPlanForToken 从 tokenKey 查套餐配置
// 返回 nil 表示无套餐或无限制（回退默认阈值）
func lookupPlanForToken(tokenKey string) *model.Plan {
	if tokenKey == "" {
		return nil
	}
	token, err := model.FindByKey(tokenKey)
	if err != nil || token.ID == 0 {
		log.Printf("[压缩] token 查询失败: %v", err)
		return nil
	}
	// 优先用 PlanName，回退 RateLimitGroup
	planName := token.PlanName
	if planName == "" {
		planName = token.RateLimitGroup
	}
	if planName == "" {
		return nil
	}
	plan, err := GetPlan(planName)
	if err != nil {
		log.Printf("[压缩] 套餐查询失败 %s: %v", planName, err)
		return nil
	}
	return plan
}

// splitMessagesSafe 将消息分为 system / middle / tail
// 安全处理：如果 tail 从 tool 结果开始，向前扩展到包含完整的 tool_calls 链
func splitMessagesSafe(messages []map[string]interface{}, tailKeep int) (system, middle, tail []map[string]interface{}) {
	// system 消息在开头（连续的 role=system）
	sysEnd := 0
	for i, msg := range messages {
		if role, _ := msg["role"].(string); role == "system" {
			sysEnd = i + 1
		} else {
			break
		}
	}

	tailStart := len(messages) - tailKeep
	if tailStart < sysEnd {
		tailStart = sysEnd
	}

	// 安全检查：向前扩展 tail，确保不切断 tool_calls 链
	// 如果 tail 第一条是 role=tool，需要往前找到对应的 assistant(tool_calls)
	for tailStart > sysEnd {
		firstTail := messages[tailStart]
		firstRole, _ := firstTail["role"].(string)

		if firstRole == "tool" {
			// tool 结果 → 必须往前找对应的 assistant(tool_calls)
			tailStart--
			continue
		}
		if firstRole == "assistant" {
			if _, ok := firstTail["tool_calls"]; ok {
				// 这是 tool_calls 起点 → 完整链，停
				break
			}
		}
		// 非工具链 → 安全
		break
	}

	system = messages[:sysEnd]
	middle = messages[sysEnd:tailStart]
	tail = messages[tailStart:]
	return
}

// generateSummary 调用 flash 模型生成中间段摘要
func generateSummary(middleMessages []map[string]interface{}) string {
	// 提取中间消息的文本内容（跳过图片）
	var dialogParts []string
	for _, msg := range middleMessages {
		role, _ := msg["role"].(string)
		content := extractTextOnly(msg)
		if content == "" {
			continue
		}
		// 每条消息截取前 500 字，避免摘要请求本身过长
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		dialogParts = append(dialogParts, role+": "+content)
	}

	if len(dialogParts) == 0 {
		return ""
	}

	dialog := strings.Join(dialogParts, "\n")
	// 限制总长度（~24K tokens），避免摘要请求太贵
	if len(dialog) > 8000 {
		dialog = dialog[:8000] + "\n... [更多历史已省略]"
	}

	prompt := fmt.Sprintf(`请将以下对话历史压缩成不超过 500 字的摘要。
保留关键信息：用户的主要需求、已达成的共识、重要的技术决策、未解决的问题。
忽略寒暄、重复内容和无关细节。只输出摘要正文，不要加标题。

对话历史：
%s`, dialog)

	return callFlashForSummary(prompt)
}

// callFlashForSummary 调用 deepseek-v4-flash 渠道生成摘要
func callFlashForSummary(prompt string) string {
	// 从数据库找可用的 flash 渠道
	// 兼容两种配置：model_group 或 models LIKE
	var channel model.Channel
	if err := model.DB.Where(
		"(model_group = ? OR LOWER(models) LIKE ?) AND status = ?",
		"deepseek-v4-flash", "%deepseek-v4-flash%", 1,
	).Order("priority DESC").First(&channel).Error; err != nil {
		log.Printf("[压缩] 找不到 flash 渠道: %v", err)
		return ""
	}

	reqBody := map[string]interface{}{
		"model": channel.Models, // 用渠道配置的模型名（可能是 deepseek-v4-flash 或其他）
		"messages": []map[string]interface{}{
			{"role": "user", "content": prompt},
		},
		"max_tokens": SummaryMaxTokens,
		"stream":     false,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	url := channel.BaseURL
	if !strings.HasSuffix(url, "/chat/completions") {
		url += "/v1/chat/completions"
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+channel.Key)

	client := &http.Client{Timeout: SummaryTimeout * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[压缩] 摘要请求失败: %v", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		preview := string(body)
		if len(preview) > 200 {
			preview = preview[:200]
		}
		log.Printf("[压缩] 摘要请求 HTTP %d: %s", resp.StatusCode, preview)
		return ""
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("[压缩] 摘要响应解析失败: %v", err)
		return ""
	}

	if len(result.Choices) == 0 {
		return ""
	}

	summary := strings.TrimSpace(result.Choices[0].Message.Content)
	log.Printf("[压缩] 摘要生成成功, 长度=%d, 耗费 tokens=%d", len(summary), result.Usage.TotalTokens)
	return summary
}

// extractTextOnly 从消息中提取纯文本（跳过图片内容）
func extractTextOnly(msg map[string]interface{}) string {
	switch c := msg["content"].(type) {
	case string:
		return c
	case []interface{}:
		var parts []string
		for _, part := range c {
			if pm, ok := part.(map[string]interface{}); ok {
				if typ, _ := pm["type"].(string); typ == "text" {
					if t, ok := pm["text"].(string); ok {
						parts = append(parts, t)
					}
				}
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// mergeWithSummary 合并 system + 摘要 + tail
func mergeWithSummary(system []map[string]interface{}, summary string, tail []map[string]interface{}, middleCount int) []map[string]interface{} {
	summaryMsg := map[string]interface{}{
		"role":    "system",
		"content": fmt.Sprintf("[上下文摘要 · 已压缩 %d 条历史消息]\n%s", middleCount, summary),
	}

	result := make([]map[string]interface{}, 0, len(system)+1+len(tail))
	result = append(result, system...)
	result = append(result, summaryMsg)
	result = append(result, tail...)
	return result
}

// mergeTruncated 合并 system + 标记 + tail（旧模型历史裁剪）
func mergeTruncated(system, tail []map[string]interface{}, middleCount int) []map[string]interface{} {
	marker := map[string]interface{}{
		"role": "system",
		"content": fmt.Sprintf(
			"[上下文已压缩] 已省略 %d 条历史消息。保留最近对话和系统指令。如需引用之前内容，请说明。",
			middleCount,
		),
	}

	result := make([]map[string]interface{}, 0, len(system)+1+len(tail))
	result = append(result, system...)
	result = append(result, marker)
	result = append(result, tail...)
	return result
}
