package service

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ============================================================
// ImageAnalysisCache — 图片分析缓存 v2（异步转发 + 文字替换）
// AiToMoney 团队出品 🚀
// ============================================================

const (
	AnalysisTTL         = 30 * time.Minute
	WaitForAnalysisTime = 3 * time.Second
	AnalysisMaxImage    = 10 * 1024 * 1024 // 10MB
)

type AnalysisEntry struct {
	Description string
	AnalyzedAt  time.Time
}

type ImageAnalysisCache struct {
	mu      sync.RWMutex
	items   map[string]*AnalysisEntry
	pending map[string]bool
	notify  map[string]chan bool
	ttl     time.Duration
}

var GlobalImageAnalysis *ImageAnalysisCache

func InitImageAnalysisCache() {
	GlobalImageAnalysis = &ImageAnalysisCache{
		items:   make(map[string]*AnalysisEntry),
		pending: make(map[string]bool),
		notify:  make(map[string]chan bool),
		ttl:     AnalysisTTL,
	}
	go GlobalImageAnalysis.cleanupLoop()
	log.Printf("[图片分析] 初始化完成，TTL=%v", AnalysisTTL)
}

// HashMessages 计算最后一条 user 消息的 hash
func HashMessages(messages []map[string]interface{}) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if role, _ := messages[i]["role"].(string); role == "user" {
			bytes, _ := json.Marshal(messages[i])
			h := sha256.Sum256(bytes)
			return hex.EncodeToString(h[:])
		}
	}
	return ""
}

// AnalyzeAsync v2: 直接转发原始 messages 给 Qwen
func (c *ImageAnalysisCache) AnalyzeAsync(hash string, messages []map[string]interface{}) {
	c.mu.Lock()
	if _, exists := c.items[hash]; exists {
		c.mu.Unlock()
		return
	}
	if c.pending[hash] {
		c.mu.Unlock()
		return
	}
	c.pending[hash] = true
	ch := make(chan bool, 1)
	c.notify[hash] = ch
	c.mu.Unlock()

	go func() {
		desc := callQwenAnalyzeMessages(messages)
		c.mu.Lock()
		c.items[hash] = &AnalysisEntry{Description: desc, AnalyzedAt: time.Now()}
		delete(c.pending, hash)
		close(ch)
		c.mu.Unlock()
		log.Printf("[图片分析] 完成: hash=%s... desc=%s...", hash[:min2(12, len(hash))], desc[:min2(50, len(desc))])
	}()
}

func (c *ImageAnalysisCache) WaitForAnalysis(hash string, timeout time.Duration) bool {
	c.mu.RLock()
	if _, ok := c.items[hash]; ok {
		c.mu.RUnlock()
		return true
	}
	ch, ok := c.notify[hash]
	c.mu.RUnlock()
	if !ok {
		return false
	}
	select {
	case <-ch:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (c *ImageAnalysisCache) GetAnalysis(hash string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if entry, ok := c.items[hash]; ok {
		if time.Since(entry.AnalyzedAt) < c.ttl {
			return entry.Description
		}
	}
	return ""
}

func (c *ImageAnalysisCache) HasAnalysis(hash string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, hasItem := c.items[hash]
	_, hasPending := c.pending[hash]
	return hasItem || hasPending
}

// ReplaceImagesWithText 遍历消息，把已知分析的图片替换为文字描述
func ReplaceImagesWithText(messages []map[string]interface{}) []map[string]interface{} {
	if GlobalImageAnalysis == nil {
		return messages
	}
	result := make([]map[string]interface{}, len(messages))
	for i, msg := range messages {
		newMsg := make(map[string]interface{})
		for k, v := range msg {
			newMsg[k] = v
		}
		content, ok := newMsg["content"]
		if !ok {
			result[i] = newMsg
			continue
		}
		switch c := content.(type) {
		case []interface{}:
			newParts := make([]interface{}, 0, len(c))
			for _, part := range c {
				if pm, ok := part.(map[string]interface{}); ok {
					if typ, _ := pm["type"].(string); typ == "image_url" || typ == "image" {
						hash := hashFromContent(pm)
						desc := GlobalImageAnalysis.GetAnalysis(hash)
						if desc != "" {
							newParts = append(newParts, map[string]interface{}{
								"type": "text",
								"text": fmt.Sprintf("[图片内容：%s]", desc),
							})
						} else if GlobalImageAnalysis.HasAnalysis(hash) {
							if GlobalImageAnalysis.WaitForAnalysis(hash, WaitForAnalysisTime) {
								desc = GlobalImageAnalysis.GetAnalysis(hash)
								newParts = append(newParts, map[string]interface{}{
									"type": "text",
									"text": fmt.Sprintf("[图片内容：%s]", desc),
								})
							} else {
								newParts = append(newParts, map[string]interface{}{
									"type": "text",
									"text": "[图片分析中，请稍后]",
								})
							}
						} else {
							newParts = append(newParts, part)
						}
					} else {
						newParts = append(newParts, part)
					}
				} else {
					newParts = append(newParts, part)
				}
			}
			newMsg["content"] = newParts
		case string:
			if strings.HasPrefix(c, "data:image") {
				imgBytes, err := decodeBase64Image(c)
				if err == nil {
					h := sha256.Sum256(imgBytes)
					hash := hex.EncodeToString(h[:])
					desc := GlobalImageAnalysis.GetAnalysis(hash)
					if desc != "" {
						newMsg["content"] = fmt.Sprintf("[图片内容：%s]", desc)
					}
				}
			}
		}
		result[i] = newMsg
	}
	return result
}

func (c *ImageAnalysisCache) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.items {
			if now.Sub(v.AnalyzedAt) > c.ttl {
				delete(c.items, k)
			}
		}
		c.mu.Unlock()
	}
}

// ============================================================
// 内部工具
// ============================================================

func hashFromContent(pm map[string]interface{}) string {
	if urlMap, ok := pm["image_url"].(map[string]interface{}); ok {
		if url, ok := urlMap["url"].(string); ok {
			if strings.HasPrefix(url, "data:image") {
				imgBytes, err := decodeBase64Image(url)
				if err == nil {
					h := sha256.Sum256(imgBytes)
					return hex.EncodeToString(h[:])
				}
			}
			h := sha256.Sum256([]byte(url))
			return hex.EncodeToString(h[:])
		}
	}
	bytes, _ := json.Marshal(pm)
	h := sha256.Sum256(bytes)
	return hex.EncodeToString(h[:])
}

func callQwenAnalyzeMessages(messages []map[string]interface{}) string {
	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	if apiKey == "" {
		return "[图片分析失败：未配置 API Key]"
	}

	analyzeMsg := map[string]interface{}{
		"role": "user",
		"content": "请根据上面的图片，详细描述所有可见内容：1.场景和构图 2.所有可见文字（完整转写）3.主要物体、颜色、数量 4.位置关系 5.如果是截图描述界面数据。要求准确完整，300-500字。",
	}

	allMessages := make([]interface{}, 0, len(messages)+1)
	for _, m := range messages {
		allMessages = append(allMessages, m)
	}
	allMessages = append(allMessages, analyzeMsg)

	reqBody := map[string]interface{}{
		"model":      "qwen3.7-plus",
		"messages":   allMessages,
		"max_tokens": 1024,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	resp, err := http.Post(
		"https://coding.dashscope.aliyuncs.com/v1/chat/completions",
		"application/json",
		strings.NewReader(string(bodyBytes)),
	)
	if err != nil {
		log.Printf("[图片分析] Qwen 调用失败: %v", err)
		return "[图片分析失败：网络错误]"
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[图片分析] Qwen 返回 %d: %s", resp.StatusCode, string(body)[:min2(200, len(body))])
		return "[图片分析失败：API 错误]"
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result.Choices) > 0 {
		return result.Choices[0].Message.Content
	}
	return "[图片分析失败：无响应]"
}

func decodeBase64Image(dataURL string) ([]byte, error) {
	idx := strings.Index(dataURL, "base64,")
	if idx < 0 {
		return nil, fmt.Errorf("invalid data URL")
	}
	return base64.StdEncoding.DecodeString(dataURL[idx+7:])
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}
