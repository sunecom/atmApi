package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

type CacheEntry struct {
	Response  []byte
	CreatedAt time.Time
	HitCount  int
}

type ResponseCache struct {
	entries map[string]*CacheEntry
	mu      sync.RWMutex
	ttl     time.Duration
	maxSize int
}

var GlobalCache *ResponseCache

func InitCache(ttl time.Duration, maxSize int) {
	GlobalCache = &ResponseCache{
		entries: make(map[string]*CacheEntry),
		ttl:     ttl,
		maxSize: maxSize,
	}
	// 启动清理协程
	go GlobalCache.cleanup()
}

func (c *ResponseCache) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for key, entry := range c.entries {
			if now.Sub(entry.CreatedAt) > c.ttl {
				delete(c.entries, key)
			}
		}
		c.mu.Unlock()
	}
}

func (c *ResponseCache) GenerateKey(tokenKey, model string, messages []map[string]interface{}) string {
	// 序列化请求内容
	data := map[string]interface{}{
		"token":    tokenKey,
		"model":    model,
		"messages": messages,
	}
	jsonData, _ := json.Marshal(data)
	hash := sha256.Sum256(jsonData)
	return hex.EncodeToString(hash[:])
}

func (c *ResponseCache) Get(key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	entry, exists := c.entries[key]
	if !exists {
		return nil, false
	}
	
	// 检查是否过期
	if time.Since(entry.CreatedAt) > c.ttl {
		return nil, false
	}
	
	entry.HitCount++
	return entry.Response, true
}

func (c *ResponseCache) Set(key string, response []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	// 如果超过最大容量，删除最旧的
	if len(c.entries) >= c.maxSize {
		var oldestKey string
		var oldestTime time.Time
		for k, v := range c.entries {
			if oldestKey == "" || v.CreatedAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.CreatedAt
			}
		}
		delete(c.entries, oldestKey)
	}
	
	c.entries[key] = &CacheEntry{
		Response:  response,
		CreatedAt: time.Now(),
		HitCount:  0,
	}
}

func (c *ResponseCache) Stats() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	totalHits := 0
	for _, entry := range c.entries {
		totalHits += entry.HitCount
	}
	
	return map[string]interface{}{
		"size":       len(c.entries),
		"max_size":   c.maxSize,
		"ttl":        c.ttl.String(),
		"total_hits": totalHits,
	}
}

func GetCacheStats() map[string]interface{} {
	if GlobalCache == nil {
		return map[string]interface{}{
			"error": "cache not initialized",
		}
	}
	return GlobalCache.Stats()
}
