package service

import (
	"log"
	"regexp"
	"strings"
)

// hasImageURL 检查文本中是否包含图片 URL
var imageURLRegex = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp|svg|bmp|ico)(\?[^\s]*)?`)

func hasImageURL(text string) bool {
	// 查找 http/https 开头的 URL 且以图片扩展名结尾
	urlRegex := regexp.MustCompile(`(?i)https?://[^\s]+\.(png|jpe?g|gif|webp|svg|bmp|ico)(\?[^\s]*)?`)
	return urlRegex.MatchString(text)
}

// SmartRoute 根据请求内容智能选择模型
// 优先级：0. 有 tool_calls/tool 消息 → 锁定模型（避免跨模型 tool_call 不兼容）
//         1. 图片消息 → qwen3.7-plus（多模态）
//         2. 纯文本：简单 → deepseek-v4-flash（便宜）
//         3. 纯文本：复杂 → deepseek-v4-pro（深度推理）
// 仅对 deepseek-a4 做智能路由，其他模型尊重用户选择
// tokenKey: 用于会话级模型偏好记忆（有 tool_calls 时优先复用上次模型）
func SmartRoute(requestedModel string, messages []map[string]interface{}, tokenKey string) string {
	// 统一转小写
	requestedModel = strings.ToLower(requestedModel)

	// 只对 deepseek-a4 做智能路由
	if requestedModel != "deepseek-a4" {
		return requestedModel
	}

	// 图片检测（只看用户消息）
	hasImg := HasImageContent(messages)
	if hasImg {
		return "qwen3.7-plus"
	}

	// 🧠 会话模型偏好：有 tool_calls 时优先复用上次模型
	// 避免跨模型切换导致 tool_calls 格式不兼容
	if hasToolCalls(messages) && GlobalModelPref != nil {
		if preferred := GlobalModelPref.GetPreferredModel(tokenKey); preferred != "" {
			log.Printf("[路由] tool_calls存在 → 复用偏好模型: %s", preferred)
			return preferred
		}
	}

	// 纯文本，分析复杂度
	complexity := analyzeComplexityV2(messages)

	switch complexity {
	case "simple":
		return "deepseek-v4-flash"
	case "complex":
		return "deepseek-v4-pro"
	default:
		return "deepseek-v4-flash"
	}
}

// hasToolCalls 检查消息中是否有进行中的工具调用
func hasToolCalls(messages []map[string]interface{}) bool {
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		if role == "tool" || role == "assistant" {
			if _, ok := msg["tool_calls"]; ok && role == "assistant" {
				return true
			}
			if role == "tool" {
				return true
			}
		}
	}
	return false
}

// HasImageContent 检查**最后一条 user 消息**中是否包含图片
// 不扫描历史消息，避免旧的图片标记影响新请求的路由
func HasImageContent(messages []map[string]interface{}) bool {
	// 从后往前找最后一条 user 消息
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		role, _ := msg["role"].(string)
		if role != "user" {
			continue
		}
		// 找到最后一条 user 消息，只检查这一条
		content, ok := msg["content"]
		if !ok {
			return false
		}
		switch c := content.(type) {
		case string:
			if strings.Contains(c, "data:image") {
				return true
			}
			if hasImageURL(c) {
				return true
			}
		case []interface{}:
			for _, part := range c {
				if partMap, ok := part.(map[string]interface{}); ok {
					if typ, _ := partMap["type"].(string); typ == "image_url" || typ == "image" {
						return true
					}
					if imageURL, ok := partMap["image_url"].(map[string]interface{}); ok {
						if url, ok := imageURL["url"].(string); ok {
							if strings.Contains(url, "data:image") {
								return true
							}
						}
					}
				}
			}
		}
		return false
	}
	return false
}
// analyzeComplexityV2 分析请求复杂度
func analyzeComplexityV2(messages []map[string]interface{}) string {
	if len(messages) == 0 {
		return "simple"
	}

	// 获取最后一条用户消息
	var lastUserMsg string
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if role, ok := msg["role"].(string); ok && role == "user" {
			content, ok := msg["content"].(string)
			if ok {
				lastUserMsg = content
				break
			}
			// 也可能是数组格式（多模态）
			if contentArr, ok := msg["content"].([]interface{}); ok {
				for _, part := range contentArr {
					if partMap, ok := part.(map[string]interface{}); ok {
						if typ, ok := partMap["type"].(string); ok && typ == "text" {
							if text, ok := partMap["text"].(string); ok {
								lastUserMsg += text
							}
						}
					}
				}
				if lastUserMsg != "" {
					break
				}
			}
		}
	}

	if lastUserMsg == "" {
		return "simple"
	}

	contentLen := len(lastUserMsg)

	// 包含代码、分析、设计等关键词 → complex（用 Pro）
	// 优先判断，不受长度限制（短文本也可能是复杂任务）
	complexKeywords := []string{
		// 代码相关
		"代码", "函数", "方法", "类", "接口", "模块", "组件",
		"写个", "写一个", "帮我写", "实现", "编写", "开发",
		"Python", "Java", "Go", "JavaScript", "TypeScript", "SQL",
		"API", "接口", "数据库", "表结构", "索引",
		// 分析设计相关
		"分析", "设计", "架构", "优化", "重构", "解释",
		"详细", "完整", "算法", "调试", "bug", "错误",
		"比较", "区别", "方案", "设计模式", "并发",
		"性能", "安全", "测试", "部署",
	}
	for _, kw := range complexKeywords {
		if strings.Contains(lastUserMsg, kw) {
			return "complex"
		}
	}

	// 很短的问题（且没有复杂关键词）→ simple（用 Flash）
	if contentLen < 50 {
		return "simple"
	}

	// 中等长度 → simple（用 Flash）
	if contentLen < 200 {
		return "simple"
	}

	return "complex"
}

// detectLastToolCallModel 检测消息历史中是否有 tool_calls，并判断来源模型
// DeepSeek tool_call ID 格式: call_xxxxxxxx（"call_"前缀 + 7+位）
// Qwen tool_call ID 格式: 纯数字 + 字母短串，通常无 "call_" 前缀
func detectLastToolCallModel(messages []map[string]interface{}) string {
	for _, msg := range messages {
		role, _ := msg["role"].(string)

		if role == "assistant" {
			if tc, ok := msg["tool_calls"]; ok && tc != nil {
				if tcList, ok := tc.([]interface{}); ok && len(tcList) > 0 {
					if tcMap, ok := tcList[0].(map[string]interface{}); ok {
						if id, ok := tcMap["id"].(string); ok && id != "" {
							if strings.HasPrefix(id, "call_") && len(id) >= 10 {
								return "deepseek-v4-pro"
							}
						}
					}
					// 默认锁 deepseek-v4-pro（1M 上下文，更快更稳）
					return "deepseek-v4-pro"
				}
			}
		}

		if role == "tool" {
			// tool 消息 → 有工具调用，锁 deepseek-v4-pro
			return "deepseek-v4-pro"
		}
	}
	return ""
}

// GetAlternativeModels 获取模型降级失败时的同级备选模型
func GetAlternativeModels(model string) []string {
	switch model {
	case "qwen3.7-plus":
		// Qwen 备选
		return []string{"qwen3.5-plus"}
	case "deepseek-v4-flash":
		// Flash 备选
		return []string{"qwen3.5-plus"}
	case "deepseek-v4-pro":
		// Pro 备选
		return []string{"qwen3.7-plus"}
	default:
		return nil
	}
}
