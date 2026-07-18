package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCanonicalModelName V1.7: 规范模型名映射
func TestCanonicalModelName(t *testing.T) {
	tests := []struct {
		actual string
		want   string
	}{
		{"deepseek/deepseek-v4-flash", "deepseek-v4-flash"},
		{"deepseek/deepseek-v4-pro", "deepseek-v4-pro"},
		{"deepseek-v4-flash", "deepseek-v4-flash"},
		{"deepseek-v4-pro", "deepseek-v4-pro"},
		{"qwen3.7-plus", "qwen3.7-plus"},
		{"deepseek-a4", "deepseek-a4"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.actual, func(t *testing.T) {
			got := CanonicalModelName(tt.actual)
			if got != tt.want {
				t.Errorf("CanonicalModelName(%q) = %q, want %q", tt.actual, got, tt.want)
			}
		})
	}
}

// TestCommitSessionPreference_Integration V1.7: handler→偏好缓存集成测试
// 模拟真实 HTTP 请求链路：SmartRoute → 偏好写入
func TestCommitSessionPreference_Integration(t *testing.T) {
	InitModelPreferenceCache(30)
	header := http.Header{}
	header.Set("X-Atm-Session-Id", "integration-test-session")
	ctx := ResolveSession(header, 1)
	prefKey := PreferenceCacheKey(ctx.SessionHash)

	// 场景1: 首轮复杂任务 → SmartRoute 选 pro → commitSessionPreference 写入 pro
	messages1 := []map[string]interface{}{
		{"role": "user", "content": "帮我写一个 Python 脚本实现 HTTP 服务器"},
	}
	model1 := SmartRoute("deepseek-a4", messages1, "test-token", "pro", ctx.SessionHash)

	// 模拟 processResult 成功后写入偏好
	canonical1 := CanonicalModelName(model1)
	if IsValidPreferenceModel(canonical1) {
		GlobalModelPref.SetPreferredModel(prefKey, canonical1)
	}

	if got := GlobalModelPref.GetPreferredModel(prefKey); got != model1 {
		t.Errorf("场景1: 期望偏好 %s, got %s", model1, got)
	}

	// 场景2: 后续追问 → SmartRoute 应复用
	messages2 := []map[string]interface{}{
		{"role": "user", "content": "好的"},
		{"role": "assistant", "content": "好的"},
		{"role": "user", "content": "继续"},
	}
	model2 := SmartRoute("deepseek-a4", messages2, "test-token", "pro", ctx.SessionHash)

	if model1 != model2 {
		t.Errorf("场景2: 会话粘性失败 %s != %s", model1, model2)
	}

	// 场景3: 不同 session 不互相影响
	header2 := http.Header{}
	header2.Set("X-Atm-Session-Id", "other-session")
	ctx2 := ResolveSession(header2, 1)
	prefKey2 := PreferenceCacheKey(ctx2.SessionHash)

	model3 := SmartRoute("deepseek-a4", messages2, "test-token", "pro", ctx2.SessionHash)
	// 新 session 没有偏好 → 应该自然路由
	if GlobalModelPref.GetPreferredModel(prefKey2) != "" {
		t.Errorf("场景3: 新 session 不应该有偏好")
	}
	_ = model3 // 可能是 flash 或 pro
}

// TestCommitSessionPreference_SSELifecycle V1.7: SSE 生命周期模拟
func TestCommitSessionPreference_SSELifecycle(t *testing.T) {
	InitModelPreferenceCache(30)

	header := http.Header{}
	header.Set("X-Atm-Session-Id", "sse-lifecycle-test")
	ctx := ResolveSession(header, 1)
	prefKey := PreferenceCacheKey(ctx.SessionHash)

	// 模拟 SSE 流：content → [DONE]
	sseTerm := SSETermination{}
	sseTerm.ParseSSEChunk(`{"choices":[{"delta":{"content":"hello"}}]}`)
	sseTerm.ParseSSEChunk("[DONE]")

	if !sseTerm.IsLegalSuccess() {
		t.Error("content + DONE 应该是合法终态")
	}

	// 合法终态 → 写入偏好
	canonical := "deepseek-v4-pro"
	if IsValidPreferenceModel(canonical) && sseTerm.IsLegalSuccess() {
		GlobalModelPref.SetPreferredModel(prefKey, canonical)
	}

	if got := GlobalModelPref.GetPreferredModel(prefKey); got != "deepseek-v4-pro" {
		t.Errorf("SSE 合法终态应写入偏好, got %s", got)
	}
}

// TestCommitSessionPreference_SSEBroken V1.7: SSE 断流不写入
func TestCommitSessionPreference_SSEBroken(t *testing.T) {
	InitModelPreferenceCache(30)

	header := http.Header{}
	header.Set("X-Atm-Session-Id", "sse-broken-test")
	ctx := ResolveSession(header, 1)
	prefKey := PreferenceCacheKey(ctx.SessionHash)

	// 模拟 SSE 断流：有 content 但没有 [DONE]
	sseTerm := SSETermination{}
	sseTerm.ParseSSEChunk(`{"choices":[{"delta":{"content":"hello"}}]}`)
	// 没有 [DONE]，模拟断流
	sseTerm.ReadError = true

	if sseTerm.IsLegalSuccess() {
		t.Error("断流不应该合法成功")
	}

	// 不写入偏好
	if sseTerm.IsLegalSuccess() {
		GlobalModelPref.SetPreferredModel(prefKey, "deepseek-v4-pro")
	}

	if got := GlobalModelPref.GetPreferredModel(prefKey); got != "" {
		t.Errorf("断流不应写入偏好, got %s", got)
	}
}

// TestSSETermination_ProtocolError V1.7: 损坏 SSE 设置 ProtocolError
func TestSSETermination_ProtocolError(t *testing.T) {
	s := SSETermination{}
	s.ParseSSEChunk(`{broken json`)
	s.ParseSSEChunk("[DONE]")

	if !s.ProtocolError {
		t.Error("损坏 JSON 应该设置 ProtocolError")
	}
	if s.IsLegalSuccess() {
		t.Error("有 ProtocolError 不应该合法成功")
	}
}

// TestNonStreamLifecycle_ToolCalls V1.7: 非流式 tool_calls 生命周期
func TestNonStreamLifecycle_ToolCalls(t *testing.T) {
	// 模拟合法 tool_calls 响应
	body := []byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"get_weather"}}]}}]}`)
	n := ParseNonStreamResponse(body)

	if !n.HasToolCalls {
		t.Error("应该检测到 tool_calls")
	}
	if !n.IsLegalSuccess() {
		t.Error("合法 tool_calls 应该成功")
	}
}

// TestHTTPServerMock_SSE V1.7: 使用 httptest 模拟完整 SSE 流
func TestHTTPServerMock_SSE(t *testing.T) {
	// 创建模拟 SSE 服务器
	sseData := `data: {"choices":[{"delta":{"content":"hello"}}]}` + "\n" +
		`data: {"choices":[{"delta":{"content":" world"}}]}` + "\n" +
		`data: [DONE]` + "\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sseData))
	}))
	defer server.Close()

	// 从模拟服务器读取 SSE 流
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 解析 SSE 流
	sseTerm := SSETermination{}
	buf := make([]byte, 4096)
	var allData strings.Builder

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			allData.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}

	// 逐行解析
	for _, line := range strings.Split(allData.String(), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			sseTerm.ParseSSEChunk(data)
		}
	}

	if !sseTerm.SawDone {
		t.Error("应该收到 [DONE]")
	}
	if !sseTerm.SawContent {
		t.Error("应该收到 content")
	}
	if !sseTerm.IsLegalSuccess() {
		t.Error("完整 SSE 流应该合法成功")
	}
}
