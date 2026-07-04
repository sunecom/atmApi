package service

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"
)

// MaxImageSize 单张图片最大字节数（10MB）
const MaxImageSize = 10 * 1024 * 1024

// DefaultMaxEntries 默认最大缓存条目
const DefaultMaxEntries = 100

// ImageCacheEntry 图片缓存条目
type ImageCacheEntry struct {
	Key       string
	ImageURL  string
	Text      string
	Messages  []map[string]interface{}
	Timestamp time.Time
}

// ImageCache 图片缓存管理器（LRU + TTL）
type ImageCache struct {
	mu         sync.RWMutex
	items      map[string]*list.Element
	lru        *list.List // 前端=最近访问，后端=最久未访问
	maxEntries int
	ttl        time.Duration
}

// GlobalImageCache 全局图片缓存实例
var GlobalImageCache *ImageCache

// InitImageCache 初始化图片缓存
func InitImageCache(ttlMinutes int) {
	GlobalImageCache = &ImageCache{
		items:      make(map[string]*list.Element),
		lru:        list.New(),
		maxEntries: DefaultMaxEntries,
		ttl:        time.Duration(ttlMinutes) * time.Minute,
	}
	go GlobalImageCache.cleanupLoop()
	log.Printf("[图片缓存] 初始化完成，TTL=%d分钟, maxEntries=%d, maxSize=%dMB",
		ttlMinutes, DefaultMaxEntries, MaxImageSize/1024/1024)
}

// Store 存储图片（LRU 淘汰 + 大小限制）
// 返回 true=存储成功，false=被拒绝（图片过大或无图片）
func (c *ImageCache) Store(tokenKey string, messages []map[string]interface{}) bool {
	// 先检查图片大小
	if !c.checkImageSize(messages) {
		log.Printf("[图片缓存] 拒绝: token=%s, 图片超过 %dMB 限制",
			truncate(tokenKey, 8), MaxImageSize/1024/1024)
		return false
	}

	imageURL := extractImageURL(messages)
	if imageURL == "" {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 已存在 → 移到 LRU 前端（更新访问顺序）
	if elem, exists := c.items[tokenKey]; exists {
		c.lru.MoveToFront(elem)
		entry := elem.Value.(*ImageCacheEntry)
		entry.ImageURL = imageURL
		entry.Text = extractUserText(messages)
		entry.Messages = messages
		entry.Timestamp = time.Now()
		log.Printf("[图片缓存] 更新: token=%s, url=%s, text=%q",
			truncate(tokenKey, 8), truncate(imageURL, 50), truncate(entry.Text, 30))
		return true
	}

	// 新增 → 检查容量，满了就淘汰最久未访问的
	if c.lru.Len() >= c.maxEntries {
		c.evictOldest()
	}

	text := extractUserText(messages)
	entry := &ImageCacheEntry{
		Key:       tokenKey,
		ImageURL:  imageURL,
		Text:      text,
		Messages:  messages,
		Timestamp: time.Now(),
	}

	elem := c.lru.PushFront(entry)
	c.items[tokenKey] = elem

	log.Printf("[图片缓存] 存储: token=%s, url=%s, text=%q, 当前缓存数=%d",
		truncate(tokenKey, 8), truncate(imageURL, 50), truncate(text, 30), c.lru.Len())
	return true
}

// Retrieve 获取缓存的图片（访问时移到 LRU 前端）
func (c *ImageCache) Retrieve(tokenKey string) *ImageCacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, exists := c.items[tokenKey]
	if !exists {
		return nil
	}

	entry := elem.Value.(*ImageCacheEntry)
	if time.Since(entry.Timestamp) > c.ttl {
		// 过期 → 删除
		c.removeElement(elem)
		return nil
	}

	// 移到前端（LRU 更新）
	c.lru.MoveToFront(elem)
	return entry
}

// Remove 清除缓存
func (c *ImageCache) Remove(tokenKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, exists := c.items[tokenKey]; exists {
		c.removeElement(elem)
		log.Printf("[图片缓存] 清除: token=%s", truncate(tokenKey, 8))
	}
}

// Stats 返回缓存统计信息
func (c *ImageCache) Stats() (count int, maxEntries int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lru.Len(), c.maxEntries
}

// evictOldest 淘汰 LRU 后端（最久未访问）
// 调用方必须持有写锁
func (c *ImageCache) evictOldest() {
	elem := c.lru.Back()
	if elem == nil {
		return
	}
	entry := elem.Value.(*ImageCacheEntry)
	log.Printf("[图片缓存] LRU淘汰: token=%s, 缓存数=%d→%d",
		truncate(entry.Key, 8), c.lru.Len(), c.lru.Len()-1)
	c.removeElement(elem)
}

// removeElement 从 LRU 链表和 map 中删除
// 调用方必须持有写锁
func (c *ImageCache) removeElement(elem *list.Element) {
	entry := elem.Value.(*ImageCacheEntry)
	c.lru.Remove(elem)
	delete(c.items, entry.Key)
}

// cleanupLoop 定期清理过期条目
func (c *ImageCache) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		var toRemove []*list.Element
		for elem := c.lru.Back(); elem != nil; {
			entry := elem.Value.(*ImageCacheEntry)
			prev := elem.Prev()
			if now.Sub(entry.Timestamp) > c.ttl {
				toRemove = append(toRemove, elem)
			}
			elem = prev
		}
		for _, elem := range toRemove {
			c.removeElement(elem)
		}
		if len(toRemove) > 0 {
			log.Printf("[图片缓存] 定时清理: 移除 %d 条过期缓存, 剩余 %d 条",
				len(toRemove), c.lru.Len())
		}
		c.mu.Unlock()
	}
}

// checkImageSize 检查 messages 中图片是否超过大小限制
func (c *ImageCache) checkImageSize(messages []map[string]interface{}) bool {
	for _, msg := range messages {
		content, ok := msg["content"]
		if !ok {
			continue
		}
		switch ct := content.(type) {
		case string:
			// base64 图片：直接检查字节长度
			if strings.HasPrefix(ct, "data:image") {
				log.Printf("[图片缓存] checkImageSize string: len=%d, limit=%d", len(ct), MaxImageSize*4/3)
				if len(ct) > MaxImageSize*4/3 {
					return false
				}
			}
		case []interface{}:
			for _, part := range ct {
				if partMap, ok := part.(map[string]interface{}); ok {
					if typ, _ := partMap["type"].(string); typ == "image_url" {
						if urlMap, ok := partMap["image_url"].(map[string]interface{}); ok {
							if url, ok := urlMap["url"].(string); ok {
								if strings.HasPrefix(url, "data:image") {
									log.Printf("[图片缓存] checkImageSize array: len=%d, limit=%d", len(url), MaxImageSize*4/3)
									if len(url) > MaxImageSize*4/3 {
										return false
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return true
}

func extractImageURL(messages []map[string]interface{}) string {
	for _, msg := range messages {
		content, ok := msg["content"]
		if !ok {
			continue
		}
		switch c := content.(type) {
		case string:
			if len(c) > 100 && strings.HasPrefix(c[:10], "data:image") {
				hash := sha256.Sum256([]byte(c))
				return "base64:" + hex.EncodeToString(hash[:8])
			}
		case []interface{}:
			for _, part := range c {
				if partMap, ok := part.(map[string]interface{}); ok {
					if typ, _ := partMap["type"].(string); typ == "image_url" {
						if urlMap, ok := partMap["image_url"].(map[string]interface{}); ok {
							if url, ok := urlMap["url"].(string); ok {
								return url
							}
						}
					}
				}
			}
		}
	}
	return ""
}

func extractUserText(messages []map[string]interface{}) string {
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		if role != "user" {
			continue
		}
		content, ok := msg["content"]
		if !ok {
			continue
		}
		switch c := content.(type) {
		case string:
			trimmed := strings.TrimSpace(c)
			if len(trimmed) > 0 && !strings.HasPrefix(trimmed, "data:image") {
				return trimmed
			}
		case []interface{}:
			var texts []string
			for _, part := range c {
				if partMap, ok := part.(map[string]interface{}); ok {
					if typ, _ := partMap["type"].(string); typ == "text" {
						if text, ok := partMap["text"].(string); ok {
							t := strings.TrimSpace(text)
							if len(t) > 0 {
								texts = append(texts, t)
							}
						}
					}
				}
			}
			if len(texts) > 0 {
				return strings.Join(texts, " ")
			}
		}
	}
	return ""
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// HasImage 检查 messages 中是否包含图片
func HasImage(messages []map[string]interface{}) bool {
	return extractImageURL(messages) != ""
}

// Clear 清除缓存（Remove 的别名，兼容 routes.go 调用）
func (c *ImageCache) Clear(tokenKey string) {
	c.Remove(tokenKey)
}

// GenerateImageCacheResponse 生成纯图片缓存的模拟响应
// 当用户只发图片没发问题时，返回提示让用户提问
func GenerateImageCacheResponse() []byte {
	resp := map[string]interface{}{
		"id":      "imgcache-placeholder",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "deepseek-a4",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "图片已收到，请告诉我你想问什么？",
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		},
	}
	data, _ := json.Marshal(resp)
	return data
}

// MergeImageWithQuestion 将缓存的图片 messages 和新的文字问题合并
func MergeImageWithQuestion(cachedMessages, newMessages []map[string]interface{}) []map[string]interface{} {
	// 从缓存中提取图片部分
	var imageParts []interface{}
	for _, msg := range cachedMessages {
		content, ok := msg["content"]
		if !ok {
			continue
		}
		switch c := content.(type) {
		case string:
			if strings.HasPrefix(c, "data:image") {
				imageParts = append(imageParts, map[string]interface{}{
					"type": "image_url",
					"image_url": map[string]interface{}{
						"url": c,
					},
				})
			}
		case []interface{}:
			for _, part := range c {
				if partMap, ok := part.(map[string]interface{}); ok {
					if typ, _ := partMap["type"].(string); typ == "image_url" {
						imageParts = append(imageParts, part)
					}
				}
			}
		}
	}

	if len(imageParts) == 0 {
		return newMessages
	}

	// 将图片注入到最后一条用户消息中
	result := make([]map[string]interface{}, len(newMessages))
	copy(result, newMessages)

	for i := len(result) - 1; i >= 0; i-- {
		if role, _ := result[i]["role"].(string); role == "user" {
			content := result[i]["content"]
			var textParts []interface{}

			switch c := content.(type) {
			case string:
				textParts = append(textParts, map[string]interface{}{
					"type": "text",
					"text": c,
				})
			case []interface{}:
				textParts = append(textParts, c...)
			}

			// 图片 + 文字合并为多模态格式
			merged := append(imageParts, textParts...)
			result[i]["content"] = merged
			break
		}
	}

	return result
}
