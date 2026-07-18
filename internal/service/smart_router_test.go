package service

import (
	"testing"
	"time"
)

// TestModelPreferenceCache_BasicTTL P0-4: 测试 TTL 过期
func TestModelPreferenceCache_BasicTTL(t *testing.T) {
	cache := &ModelPreferenceCache{
		items:   make(map[string]*ModelPreference),
		ttl:     100 * time.Millisecond, // 短 TTL 用于测试
		maxSize: 100,
	}

	key := "pref:test-session"
	cache.SetPreferredModel(key, "deepseek-v4-pro")

	// 立即读取应该成功
	if got := cache.GetPreferredModel(key); got != "deepseek-v4-pro" {
		t.Errorf("期望 deepseek-v4-pro, got %s", got)
	}

	// 等待过期
	time.Sleep(150 * time.Millisecond)

	// 过期后应该返回空
	if got := cache.GetPreferredModel(key); got != "" {
		t.Errorf("期望空字符串, got %s", got)
	}
}

// TestModelPreferenceCache_SlidingTTL P0-4: 测试滑动续期
func TestModelPreferenceCache_SlidingTTL(t *testing.T) {
	cache := &ModelPreferenceCache{
		items:   make(map[string]*ModelPreference),
		ttl:     100 * time.Millisecond,
		maxSize: 100,
	}

	key := "pref:test-session"
	cache.SetPreferredModel(key, "deepseek-v4-flash")

	// 等待 50ms
	time.Sleep(50 * time.Millisecond)

	// 重新设置（续期）
	cache.SetPreferredModel(key, "deepseek-v4-pro")

	// 再等 50ms（总共 100ms，但续期后应该还有效）
	time.Sleep(50 * time.Millisecond)

	// 应该还能读到新值
	if got := cache.GetPreferredModel(key); got != "deepseek-v4-pro" {
		t.Errorf("期望 deepseek-v4-pro, got %s", got)
	}
}

// TestModelPreferenceCache_LRUEviction 测试 LRU 容量限制
func TestModelPreferenceCache_LRUEviction(t *testing.T) {
	cache := &ModelPreferenceCache{
		items:   make(map[string]*ModelPreference),
		ttl:     1 * time.Hour, // 长 TTL
		maxSize: 3,             // 小容量
	}

	// 填满
	cache.SetPreferredModel("pref:1", "model1")
	cache.SetPreferredModel("pref:2", "model2")
	cache.SetPreferredModel("pref:3", "model3")

	// 再添加一个，应该触发 LRU
	cache.SetPreferredModel("pref:4", "model4")

	// 检查容量
	if len(cache.items) > 3 {
		t.Errorf("期望最多 3 个条目, got %d", len(cache.items))
	}
}

// TestModelPreferenceCache_RealSlidingTTL P0-4 V1.2: 测试真滑动 TTL（Get 命中时续期）
func TestModelPreferenceCache_RealSlidingTTL(t *testing.T) {
	cache := &ModelPreferenceCache{
		items:   make(map[string]*ModelPreference),
		ttl:     100 * time.Millisecond,
		maxSize: 100,
	}

	key := "pref:test-sliding"
	cache.SetPreferredModel(key, "deepseek-v4-flash")

	// 等待 60ms（超过一半 TTL）
	time.Sleep(60 * time.Millisecond)

	// Get 命中应该刷新时间戳
	if got := cache.GetPreferredModel(key); got != "deepseek-v4-flash" {
		t.Errorf("期望 deepseek-v4-flash, got %s", got)
	}

	// 再等待 60ms（如果没有续期，总共 120ms > 100ms，应该过期）
	time.Sleep(60 * time.Millisecond)

	// 因为 Get 时续期了，应该还能读到
	if got := cache.GetPreferredModel(key); got != "deepseek-v4-flash" {
		t.Errorf("滑动续期失败: 期望 deepseek-v4-flash, got %s", got)
	}
}

// TestHasActiveToolTransaction_ToolResponse P0-5 V1.2: tool 响应后事务仍活跃
func TestHasActiveToolTransaction_ToolResponse(t *testing.T) {
	tests := []struct {
		name     string
		messages []map[string]interface{}
		want     bool
	}{
		{
			name: "assistant(tool_calls) → tool 响应 → 事务仍活跃",
			messages: []map[string]interface{}{
				{"role": "user", "content": "查天气"},
				{"role": "assistant", "tool_calls": []interface{}{map[string]interface{}{"id": "c1"}}},
				{"role": "tool", "content": "晴天 25°C"},
			},
			want: true,
		},
		{
			name: "多轮 tool_calls → tool 响应 → tool 响应 → 仍活跃",
			messages: []map[string]interface{}{
				{"role": "user", "content": "查天气"},
				{"role": "assistant", "tool_calls": []interface{}{}},
				{"role": "tool", "content": "结果1"},
				{"role": "assistant", "tool_calls": []interface{}{}},
				{"role": "tool", "content": "结果2"},
			},
			want: true,
		},
		{
			name: "assistant(tool_calls) → tool 响应 → assistant(无tool_calls) → 事务完成",
			messages: []map[string]interface{}{
				{"role": "user", "content": "查天气"},
				{"role": "assistant", "tool_calls": []interface{}{}},
				{"role": "tool", "content": "晴天"},
				{"role": "assistant", "content": "今天是晴天"},
			},
			want: false,
		},
		{
			name: "OpenClaw 伪 user 工具结果",
			messages: []map[string]interface{}{
				{"role": "user", "content": "查天气"},
				{"role": "assistant", "tool_calls": []interface{}{}},
				{"role": "user", "content": "[tool result] 晴天 25°C"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasActiveToolTransaction(tt.messages)
			if got != tt.want {
				t.Errorf("HasActiveToolTransaction() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSmartRoute_SessionStickiness P0-3: 测试会话粘性
// V1.3: SmartRoute 首轮不写偏好，由 processResult 写入
// 这里模拟: 首轮手动 SetPreferredModel，二轮应该复用
func TestSmartRoute_SessionStickiness(t *testing.T) {
	InitModelPreferenceCache(30)
	prefKey := PreferenceCacheKey("session-stick-test")

	messages := []map[string]interface{}{
		{"role": "user", "content": "帮我写一个 Python 脚本"},
	}

	// 首轮：按复杂度选模
	model1 := SmartRoute("deepseek-a4", messages, "test-token", "pro", "session-stick-test")

	// 模拟 processResult 写入偏好（V1.3: SmartRoute 不再自己写）
	GlobalModelPref.SetPreferredModel(prefKey, model1)

	// 第二轮：应该复用
	messages2 := []map[string]interface{}{
		{"role": "user", "content": "好的"},
		{"role": "assistant", "content": "好的，我来帮你写"},
		{"role": "user", "content": "继续"},
	}
	model2 := SmartRoute("deepseek-a4", messages2, "test-token", "pro", "session-stick-test")

	if model1 != model2 {
		t.Errorf("会话粘性失败: %s != %s", model1, model2)
	}
}

// TestSmartRoute_NoSession P0-2: 测试无 session 时自然路由
func TestSmartRoute_NoSession(t *testing.T) {
	InitModelPreferenceCache(30)

	messages := []map[string]interface{}{
		{"role": "user", "content": "帮我写一个 Python 脚本"},
	}

	// 无 session hash
	model := SmartRoute("deepseek-a4", messages, "test-token", "pro", "no_session_123")

	// 应该返回 flash 或 pro（按复杂度），但不应该缓存
	if model != "deepseek-v4-flash" && model != "deepseek-v4-pro" {
		t.Errorf("无 session 应该自然路由, got %s", model)
	}
}

// TestSmartRoute_ToolTransaction P0-5: 测试工具事务锁定
// V1.3: 偏好需要外部预设
func TestSmartRoute_ToolTransaction(t *testing.T) {
	InitModelPreferenceCache(30)

	prefKey := PreferenceCacheKey("session-tool-test")
	GlobalModelPref.SetPreferredModel(prefKey, "deepseek-v4-pro")

	// 工具事务中
	messages := []map[string]interface{}{
		{"role": "user", "content": "帮我查天气"},
		{"role": "assistant", "tool_calls": []interface{}{map[string]interface{}{"id": "call_1"}}},
	}

	model := SmartRoute("deepseek-a4", messages, "test-token", "pro", "session-tool-test")

	// 应该强制复用偏好模型
	if model != "deepseek-v4-pro" {
		t.Errorf("工具事务应该锁定模型, got %s", model)
	}
}

// TestSmartRoute_ImageTemporary P0-3: 测试图片路由是临时的
// V1.3: SmartRoute 首轮不写偏好，图片不写偏好
func TestSmartRoute_ImageTemporary(t *testing.T) {
	InitModelPreferenceCache(30)

	prefKey := PreferenceCacheKey("session-image-test")
	GlobalModelPref.SetPreferredModel(prefKey, "deepseek-v4-flash")

	// 发送图片
	messages := []map[string]interface{}{
		{
			"role": "user",
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "看看这张图"},
				map[string]interface{}{
					"type":      "image_url",
					"image_url": map[string]interface{}{"url": "data:image/png;base64,abc123"},
				},
			},
		},
	}

	model := SmartRoute("deepseek-a4", messages, "test-token", "pro", "session-image-test")

	// 图片应该临时路由到 qwen
	if model != "qwen3.7-plus" {
		t.Errorf("图片应该路由到 qwen, got %s", model)
	}

	// 后续文本应该仍然复用 flash（不被图片影响）
	textMessages := []map[string]interface{}{
		{"role": "user", "content": "好的"},
	}
	model2 := SmartRoute("deepseek-a4", textMessages, "test-token", "pro", "session-image-test")

	if model2 != "deepseek-v4-flash" {
		t.Errorf("图片不应影响文本偏好, got %s, 期望 flash", model2)
	}
}
