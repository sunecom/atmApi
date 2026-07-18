package service

import (
	"sync"
	"time"
)

// ModelPreference 记录一个会话上次成功使用的模型及时间
type ModelPreference struct {
	Model     string
	Timestamp time.Time
}

// ModelPreferenceCache 会话级模型偏好缓存（Phase 1: 从 Token 级改为会话级）
// 键：PreferenceCacheKey(sessionHash) = "pref:" + sessionHash
// 逻辑：有 tool_calls 时优先复用上次模型，tool_calls 消失后恢复智能路由
type ModelPreferenceCache struct {
	mu    sync.RWMutex
	items map[string]*ModelPreference // key = "pref:" + sessionHash
	ttl   time.Duration
	maxSize int
}

var GlobalModelPref *ModelPreferenceCache

// InitModelPreferenceCache 初始化会话级模型偏好缓存
// ttlMinutes: 过期时间（Phase 1: 30 分钟）
func InitModelPreferenceCache(ttlMinutes int) {
	GlobalModelPref = &ModelPreferenceCache{
		items:   make(map[string]*ModelPreference),
		ttl:     time.Duration(ttlMinutes) * time.Minute,
		maxSize: 10000, // LRU 上限
	}
	go GlobalModelPref.cleanupLoop()
}

// GetPreferredModel 获取会话的上次使用模型（如果未过期）
// key 应为 PreferenceCacheKey(sessionHash)
func (c *ModelPreferenceCache) GetPreferredModel(key string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if pref, exists := c.items[key]; exists {
		if time.Since(pref.Timestamp) < c.ttl {
			return pref.Model
		}
	}
	return ""
}

// SetPreferredModel 记录会话的上次使用模型，滑动续期
// key 应为 PreferenceCacheKey(sessionHash)
func (c *ModelPreferenceCache) SetPreferredModel(key, model string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// LRU: 超过容量时删除最旧的
	if len(c.items) >= c.maxSize {
		var oldestKey string
		var oldestTime time.Time
		for k, v := range c.items {
			if oldestKey == "" || v.Timestamp.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.Timestamp
			}
		}
		delete(c.items, oldestKey)
	}

	c.items[key] = &ModelPreference{
		Model:     model,
		Timestamp: time.Now(), // 滑动续期：每次访问都刷新
	}
}

// ClearPreferredModel 清除会话的模型偏好缓存，立即回落自然路由
func (c *ModelPreferenceCache) ClearPreferredModel(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

func (c *ModelPreferenceCache) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.items {
			if now.Sub(v.Timestamp) > c.ttl {
				delete(c.items, k)
			}
		}
		c.mu.Unlock()
	}
}
