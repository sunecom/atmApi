package service

import (
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
// 优先级：1. 图片消息 → qwen3.7-plus（多模态）
//         2. 纯文本：简单 → deepseek-v4-flash（便宜）
//         3. 纯文本：复杂 → deepseek-v4-pro（深度推理）
// 仅对 deepseek-a4 做智能路由，其他模型尊重用户选择
func SmartRoute(requestedModel string, messages []map[string]interface{}) string {
	// 统一转小写
	requestedModel = strings.ToLower(requestedModel)

	// 只对 deepseek-a4 做智能路由
	if requestedModel != "deepseek-a4" {
		return requestedModel
	}

	// 检查有没有图片
	if HasImageContent(messages) {
		return "qwen3.7-plus"
	}

	// 纯文本，分析复杂度
	complexity := analyzeComplexityV2(messages)

	switch complexity {
	case "simple":
		return "deepseek-v4-flash" // 便宜
	case "complex":
		return "deepseek-v4-pro"   // 深度推理
	default:
		return "deepseek-v4-flash" // 默认用 Flash
	}
}

// HasImageContent 检查消息中是否包含图片（base64 或 URL）
// 检测方式：
// 1. base64 data:image/...
// 2. 文本中包含图片 URL（http(s)://...xxx.png）
// 3. 多模态格式中 type=image_url 或 type=image
func HasImageContent(messages []map[string]interface{}) bool {
	for _, msg := range messages {
		content, ok := msg["content"]
		if !ok {
			continue
		}

		switch c := content.(type) {
		case string:
			// 检查 base64 图片
			if strings.Contains(c, "data:image") {
				return true
			}
			// 检查图片 URL（.png/.jpg/.jpeg/.gif/.webp/.svg）
			if hasImageURL(c) {
				return true
			}
		case []interface{}:
			// 检查多模态格式：[{"type":"text","text":"..."},{"type":"image_url","image_url":{"url":"..."}}]
			for _, part := range c {
				if partMap, ok := part.(map[string]interface{}); ok {
					if typ, ok := partMap["type"].(string); ok && typ == "image_url" {
						return true
					}
					if typ, ok := partMap["type"].(string); ok && typ == "image" {
						return true
					}
					// 检查 image_url 对象里是否有 data:image base64
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
