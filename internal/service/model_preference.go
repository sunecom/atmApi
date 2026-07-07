package service

import (
	"sync"
	"time"
)

// ModelPreference 记录一个 token 上次成功使用的模型及时间
type ModelPreference struct {
	Model     string
	Timestamp time.Time
}

// ModelPreferenceCache 会话级模型偏好缓存
// 逻辑：有 tool_calls 时优先复用上次模型，tool_calls 消失后恢复智能路由
type ModelPreferenceCache struct {
	mu    sync.RWMutex
	items map[string]*ModelPreference
	ttl   time.Duration
}

var GlobalModelPref *ModelPreferenceCache

func InitModelPreferenceCache(ttlMinutes int) {
	GlobalModelPref = &ModelPreferenceCache{
		items: make(map[string]*ModelPreference),
		ttl:   time.Duration(ttlMinutes) * time.Minute,
	}
	go GlobalModelPref.cleanupLoop()
}

// GetPreferredModel 获取 token 的上次使用模型（如果未过期）
func (c *ModelPreferenceCache) GetPreferredModel(tokenKey string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if pref, exists := c.items[tokenKey]; exists {
		if time.Since(pref.Timestamp) < c.ttl {
			return pref.Model
		}
	}
	return ""
}

// SetPreferredModel 记录 token 的上次使用模型
func (c *ModelPreferenceCache) SetPreferredModel(tokenKey, model string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items[tokenKey] = &ModelPreference{
		Model:     model,
		Timestamp: time.Now(),
	}
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
