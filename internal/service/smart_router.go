package service

import (
	"log"
	"regexp"
	"strings"
	"sync"
)

var imageURLRegex = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp|svg|bmp|ico)(\?[^\s]*)?`)

func hasImageURL(text string) bool {
	urlRegex := regexp.MustCompile(`(?i)https?://[^\s]+\.(png|jpe?g|gif|webp|svg|bmp|ico)(\?[^\s]*)?`)
	return urlRegex.MatchString(text)
}

// ===== Pro 比例控制（套餐级）=====
// 套餐 Pro 调用比例上限
var proRatioLimits = map[string]float64{
	"basic":      0.00, // 0%，纯 Flash
	"pro":        1.00, // 不限制
	"starter":    1.00, // 不限制
	"flagship":   1.00, // 不限制
	"advanced":   1.00, // 不限制
	"enterprise": 1.00, // 不限制
}

// 统计窗口大小（最近 N 次请求）
const proRatioWindow = 20

// 每个 token 的请求历史
type tokenRequestHistory struct {
	requests []bool // true = pro, false = flash
	mu       sync.Mutex
}

var tokenHistories = make(map[string]*tokenRequestHistory)
var historiesMu sync.Mutex

// CheckProAllowed 检查当前 token 是否允许用 Pro
func CheckProAllowed(tokenKey, planName string) bool {
	limit, ok := proRatioLimits[planName]
	if !ok {
		return true // 未知套餐，不限制
	}
	if limit >= 1.0 {
		return true // 不限制
	}
	if limit <= 0.001 {
		return false // 完全禁止 Pro（basic=0%）
	}

	// 获取/创建历史记录
	historiesMu.Lock()
	history, ok := tokenHistories[tokenKey]
	if !ok {
		history = &tokenRequestHistory{}
		tokenHistories[tokenKey] = history
	}
	historiesMu.Unlock()

	history.mu.Lock()
	defer history.mu.Unlock()

	// 统计 Pro 占比（窗口未填满时也检查）
	proCount := 0
	for _, isPro := range history.requests {
		if isPro {
			proCount++
		}
	}
	// 在 20 次窗口中，pro=10% 允许 2 次 Pro
	// 用窗口大小计算绝对配额，而不是比例
	maxPro := int(limit * proRatioWindow)
	return proCount < maxPro
}

// recordRequest 记录本次请求使用的模型
func recordRequest(tokenKey string, isPro bool) {
	historiesMu.Lock()
	history, ok := tokenHistories[tokenKey]
	if !ok {
		history = &tokenRequestHistory{}
		tokenHistories[tokenKey] = history
	}
	historiesMu.Unlock()

	history.mu.Lock()
	defer history.mu.Unlock()

	history.requests = append(history.requests, isPro)
	if len(history.requests) > proRatioWindow {
		history.requests = history.requests[1:]
	}
}

func SmartRoute(requestedModel string, messages []map[string]interface{}, tokenKey string, planName string) string {
	requestedModel = strings.ToLower(requestedModel)
	if requestedModel != "deepseek-a4" {
		return requestedModel
	}

	if HasImageContent(messages) {
		log.Printf("[路由] 图片内容 → qwen3.7-plus")
		return "qwen3.7-plus"
	}

	hasTC := hasToolCalls(messages)
	log.Printf("[路由] hasToolCalls=%v, GlobalModelPref=%v", hasTC, GlobalModelPref != nil)
	if hasTC && GlobalModelPref != nil {
		if preferred := GlobalModelPref.GetPreferredModel(tokenKey); preferred != "" {
			log.Printf("[路由] tool_calls活跃 → 复用偏好模型: %s", preferred)
			return preferred
		}
		log.Printf("[路由] tool_calls活跃 → 无偏好缓存，走复杂度分析")
	}

	complexity := analyzeComplexityV2(messages)
	log.Printf("[路由] 复杂度=%s plan=%s", complexity, planName)
	switch complexity {
	case "simple":
		recordRequest(tokenKey, false)
		return "deepseek-v4-flash"
	case "complex":
		// 检查 Pro 比例限制
		if !CheckProAllowed(tokenKey, planName) {
			log.Printf("[Pro限制] token=%s plan=%s 超限，静默降级Flash", tokenKey[:min(8, len(tokenKey))], planName)
			recordRequest(tokenKey, false)
			return "deepseek-v4-flash"
		}
		recordRequest(tokenKey, true)
		return "deepseek-v4-pro"
	default:
		recordRequest(tokenKey, false)
		return "deepseek-v4-flash"
	}
}

// hasToolCalls 判断当前是否有活跃的工具调用
// 只看最后一条消息：tool=等待结果, assistant(tool_calls)=刚发出调用
// 历史 assistant(tool_calls) 不算活跃，避免永久锁定
func hasToolCalls(messages []map[string]interface{}) bool {
	if len(messages) == 0 {
		return false
	}
	last := messages[len(messages)-1]
	role, _ := last["role"].(string)
	if role == "tool" {
		return true
	}
	if role == "assistant" {
		if _, ok := last["tool_calls"]; ok {
			return true
		}
	}
	return false
}

func HasImageContent(messages []map[string]interface{}) bool {
	// 只检查最后一条 user 消息，不扫描历史
	// 历史中的图片会导致路由永久锁定在 qwen3.7-plus
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		role, _ := msg["role"].(string)
		if role != "user" {
			continue
		}
		// 找到最后一条 user 消息后，检查是否有图片
		content, ok := msg["content"]
		if !ok {
			return false
		}
		switch c := content.(type) {
		case string:
			return strings.Contains(c, "data:image") || hasImageURL(c)
		case []interface{}:
			for _, part := range c {
				if partMap, ok := part.(map[string]interface{}); ok {
					if typ, _ := partMap["type"].(string); typ == "image_url" || typ == "image" {
						return true
					}
					if imageURL, ok := partMap["image_url"].(map[string]interface{}); ok {
						if url, ok := imageURL["url"].(string); ok && strings.Contains(url, "data:image") {
							return true
						}
					}
				}
			}
		}
		// 找到最后一条 user 消息后，无论是否有图片都返回
		return false
	}
	return false
}

// getUserText 从单条消息中提取纯文字
func getUserText(msg map[string]interface{}) string {
	if content, ok := msg["content"].(string); ok {
		return content
	}
	if contentArr, ok := msg["content"].([]interface{}); ok {
		var text string
		for _, part := range contentArr {
			if partMap, ok := part.(map[string]interface{}); ok {
				if typ, _ := partMap["type"].(string); typ == "text" {
					if t, ok := partMap["text"].(string); ok {
						text += t
					}
				}
			}
		}
		return text
	}
	return ""
}

// analyzeComplexityV2 分析请求复杂度
// 2026-07-08 修复：排除工具结果注入的 user 消息
// OpenClaw 把工具结果以 role="user" 注入，只取用户真实输入来判断复杂度
func analyzeComplexityV2(messages []map[string]interface{}) string {
	if len(messages) == 0 {
		return "simple"
	}

	// 从后往前找真实用户输入（排除工具结果）
	var lastUserMsg string
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if role, _ := msg["role"].(string); role != "user" {
			continue
		}

		// 检查这条 user 前面最近的 tool/assistant
		isToolResult := false
		for j := i - 1; j >= 0; j-- {
			prevRole, _ := messages[j]["role"].(string)
			if prevRole == "tool" {
				isToolResult = true // 前面有 tool → 这条 user 是工具结果
				break
			}
			if prevRole == "assistant" {
				if _, hasTC := messages[j]["tool_calls"]; hasTC {
					isToolResult = true // assistant(tool_calls) → 工具结果
				}
				break // 遇到 assistant 就停
			}
		}
		if isToolResult {
			continue // 跳过工具结果，继续往前找
		}

		// 找到了真实用户输入
		lastUserMsg = getUserText(msg)
		break
	}

	if lastUserMsg == "" {
		return "simple"
	}

	// 剥离 OpenClaw 注入的 Sender 元数据
	// 定位 "Sender (untrusted metadata):" 这个固定标记，取其后内容
	// 比 LastIndex 更准确：后续引用内容中的代码块不影响定位
	senderIdx := strings.Index(lastUserMsg, "Sender (untrusted metadata):")
	if senderIdx >= 0 {
		afterSender := lastUserMsg[senderIdx:]
		closeIdx := strings.Index(afterSender, "\n```\n")
		if closeIdx >= 0 {
			lastUserMsg = strings.TrimSpace(afterSender[closeIdx+5:])
		}
	}

	contentLen := len(lastUserMsg)
	if contentLen > 0 {
		preview := lastUserMsg
		if len(preview) > 100 {
			preview = preview[:100] + "..."
		}
		log.Printf("[复杂度] 剥离元数据后: 长度=%d, 前100字: %s", contentLen, preview)
	}

	// 深度推理关键词 → complex
	complexKeywords := []string{
		"写个", "写一个", "帮我写", "实现", "编写", "开发",
		"Python", "Java", "Go", "JavaScript", "TypeScript", "SQL",
		"架构设计", "设计模式", "算法", "并发", "调试", "bug",
		"性能优化", "安全漏洞", "重构代码", "系统设计", "索引",
		"详细", "深度", "认真", "仔细", "全面", "深入", "彻底", "严谨",
	}
	for _, kw := range complexKeywords {
		if strings.Contains(lastUserMsg, kw) {
			return "complex"
		}
	}

	if contentLen < 200 {
		return "simple"
	}
	return "complex"
}

func detectLastToolCallModel(messages []map[string]interface{}) string {
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		if role == "assistant" {
			if tc, ok := msg["tool_calls"]; ok && tc != nil {
				return "deepseek-v4-pro"
			}
		}
		if role == "tool" {
			return "deepseek-v4-pro"
		}
	}
	return ""
}

func GetAlternativeModels(model string) []string {
	switch model {
	case "qwen3.7-plus":
		return []string{"qwen3.6-plus", "qwen3.5-plus"}
	case "deepseek-v4-flash":
		return nil
	case "deepseek-v4-pro":
		return nil
	default:
		return nil
	}
}
