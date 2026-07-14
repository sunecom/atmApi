package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"atmapi/internal/glmoptimizer"
)

type CacheEntry struct {
	Response  []byte
	CreatedAt time.Time
	HitCount  int
}

type ResponseCache struct {
	entries   map[string]*CacheEntry
	order     []string // LRU 顺序（最近使用的在后面）
	mu        sync.RWMutex
	ttl       time.Duration
	maxSize   int
	hitCount  int64
	missCount int64
}

var GlobalCache *ResponseCache
var GlobalGLM52Cache *glmoptimizer.GLM52ResponseCache
var GlobalGLM52Flights *glmoptimizer.FlightGroup

func InitCache(ttl time.Duration, maxSize int) {
	GlobalCache = &ResponseCache{
		entries: make(map[string]*CacheEntry),
		order:   make([]string, 0),
		ttl:     ttl,
		maxSize: maxSize,
	}
	GlobalGLM52Cache = glmoptimizer.NewResponseCache(glmoptimizer.CacheConfig{
		TTL:           ttl,
		MaxEntries:    maxSize,
		MaxEntryBytes: 2 << 20,
		MaxTotalBytes: 64 << 20,
	})
	GlobalGLM52Flights = &glmoptimizer.FlightGroup{}
	// 启动清理协程
	go GlobalCache.cleanup()
}

type glm52RouteSnapshot struct {
	StatusCode  int         `json:"status_code"`
	Header      http.Header `json:"header"`
	Body        []byte      `json:"body"`
	ChannelID   uint        `json:"channel_id"`
	ChannelName string      `json:"channel_name"`
	AtmModel    string      `json:"atm_model"`
	ActualModel string      `json:"actual_model"`
}

func snapshotGLM52Route(result *RouteRequestResult) ([]byte, error) {
	if result == nil || result.Response == nil || result.Response.Body == nil {
		return nil, fmt.Errorf("cannot snapshot empty GLM-5.2 response")
	}
	body, err := io.ReadAll(result.Response.Body)
	_ = result.Response.Body.Close()
	if err != nil {
		return nil, err
	}
	snapshot := glm52RouteSnapshot{
		StatusCode: result.Response.StatusCode, Header: result.Response.Header.Clone(), Body: body,
		ChannelID: result.ChannelID, ChannelName: result.ChannelName,
		AtmModel: result.AtmModel, ActualModel: result.ActualModel,
	}
	return json.Marshal(snapshot)
}

func restoreGLM52Route(snapshotBytes []byte) (*RouteRequestResult, error) {
	var snapshot glm52RouteSnapshot
	if err := json.Unmarshal(snapshotBytes, &snapshot); err != nil {
		return nil, fmt.Errorf("decode coalesced GLM-5.2 response: %w", err)
	}
	response := &http.Response{
		StatusCode:    snapshot.StatusCode,
		Header:        snapshot.Header.Clone(),
		Body:          io.NopCloser(bytes.NewReader(snapshot.Body)),
		ContentLength: int64(len(snapshot.Body)),
	}
	return &RouteRequestResult{
		Response: response, ChannelID: snapshot.ChannelID, ChannelName: snapshot.ChannelName,
		AtmModel: snapshot.AtmModel, ActualModel: snapshot.ActualModel,
	}, nil
}

func coalesceGLM52Route(ctx context.Context, cacheKey string, route func() (*RouteRequestResult, error)) (*RouteRequestResult, bool, error) {
	if cacheKey == "" || GlobalGLM52Flights == nil {
		result, err := route()
		return result, false, err
	}
	snapshot, shared, err := GlobalGLM52Flights.Do(ctx, cacheKey, func() ([]byte, error) {
		result, routeErr := route()
		if routeErr != nil {
			return nil, routeErr
		}
		encoded, snapshotErr := snapshotGLM52Route(result)
		if snapshotErr != nil {
			return nil, snapshotErr
		}
		var decoded glm52RouteSnapshot
		if json.Unmarshal(encoded, &decoded) == nil {
			responseNoStore := strings.Contains(strings.ToLower(decoded.Header.Get("Cache-Control")), "no-store")
			if decoded.StatusCode == http.StatusOK && !responseNoStore && GlobalGLM52Cache != nil {
				GlobalGLM52Cache.Set(cacheKey, decoded.Body)
			}
		}
		return encoded, nil
	})
	if err != nil {
		return nil, shared, err
	}
	result, err := restoreGLM52Route(snapshot)
	return result, shared, err
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

func (c *ResponseCache) GenerateKey(tokenKey, model string, messages []map[string]interface{}, temperature float64, maxTokens int) string {
	// 序列化请求内容（包含 temperature 和 max_tokens）
	data := map[string]interface{}{
		"token":       tokenKey,
		"model":       model,
		"messages":    messages,
		"temperature": temperature,
		"max_tokens":  maxTokens,
	}
	jsonData, _ := json.Marshal(data)
	hash := sha256.Sum256(jsonData)
	return hex.EncodeToString(hash[:])
}

func (c *ResponseCache) Get(key string) ([]byte, bool) {
	c.mu.RLock()
	entry, exists := c.entries[key]
	c.mu.RUnlock()

	if !exists {
		c.mu.Lock()
		c.missCount++
		c.mu.Unlock()
		return nil, false
	}

	// 检查是否过期
	if time.Since(entry.CreatedAt) > c.ttl {
		c.mu.Lock()
		delete(c.entries, key)
		c.removeFromOrder(key)
		c.missCount++
		c.mu.Unlock()
		return nil, false
	}

	// 命中，更新 LRU 顺序和命中计数
	c.mu.Lock()
	entry.HitCount++
	c.hitCount++
	c.moveToEnd(key)
	c.mu.Unlock()

	return entry.Response, true
}

func (c *ResponseCache) Set(key string, response []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 如果已存在，更新
	if _, exists := c.entries[key]; exists {
		c.entries[key].Response = response
		c.entries[key].CreatedAt = time.Now()
		c.moveToEnd(key)
		return
	}

	// 如果超过最大容量，淘汰最久未使用的（LRU）
	if len(c.entries) >= c.maxSize {
		c.evictLRU()
	}

	c.entries[key] = &CacheEntry{
		Response:  response,
		CreatedAt: time.Now(),
		HitCount:  0,
	}
	c.order = append(c.order, key)
}

func (c *ResponseCache) Stats() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	hitRate := float64(0)
	total := c.hitCount + c.missCount
	if total > 0 {
		hitRate = float64(c.hitCount) / float64(total) * 100
	}

	return map[string]interface{}{
		"size":       len(c.entries),
		"max_size":   c.maxSize,
		"ttl":        c.ttl.String(),
		"hit_count":  c.hitCount,
		"miss_count": c.missCount,
		"hit_rate":   hitRate,
	}
}

// moveToEnd 将 key 移到 LRU 顺序末尾
func (c *ResponseCache) moveToEnd(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			c.order = append(c.order, key)
			return
		}
	}
}

// removeFromOrder 从 LRU 顺序中移除
func (c *ResponseCache) removeFromOrder(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

// evictLRU 淘汰最久未使用的条目
func (c *ResponseCache) evictLRU() {
	if len(c.order) == 0 {
		return
	}
	oldest := c.order[0]
	delete(c.entries, oldest)
	c.order = c.order[1:]
}

// ShouldCache 判断是否应该缓存该请求
// 适用：temperature=0 的确定性任务
// 不适用：temperature>0 的随机性任务
func ShouldCache(temperature float64) bool {
	return temperature == 0
}

func GetCacheStats() map[string]interface{} {
	if GlobalCache == nil {
		return map[string]interface{}{
			"error": "cache not initialized",
		}
	}
	return GlobalCache.Stats()
}

// ===== 套餐到期预警缓存(每天每token只提醒一次)=====

type ExpiryWarnCache struct {
	entries map[string]time.Time
	mu      sync.RWMutex
}

var GlobalExpiryWarnCache = &ExpiryWarnCache{
	entries: make(map[string]time.Time),
}

// ShouldWarn 检查某 token 今天是否应该发送到期提醒
func (c *ExpiryWarnCache) ShouldWarn(tokenID uint) bool {
	c.mu.RLock()
	key := fmt.Sprintf("%d_%s", tokenID, time.Now().Format("2006-01-02"))
	_, exists := c.entries[key]
	c.mu.RUnlock()

	// 清理过期条目(超过2天的)
	c.mu.Lock()
	for k, t := range c.entries {
		if time.Since(t) > 48*time.Hour {
			delete(c.entries, k)
		}
	}
	c.mu.Unlock()

	return !exists
}

// MarkWarned 标记某 token 今天已发送提醒
func (c *ExpiryWarnCache) MarkWarned(tokenID uint) {
	c.mu.Lock()
	key := fmt.Sprintf("%d_%s", tokenID, time.Now().Format("2006-01-02"))
	c.entries[key] = time.Now()
	c.mu.Unlock()
}
