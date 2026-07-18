package service

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestSSETermination_ContentDone V1.5: content + [DONE] = 合法成功
func TestSSETermination_ContentDone(t *testing.T) {
	s := SSETermination{}
	s.ParseSSEChunk(`{"choices":[{"delta":{"content":"hello"}}]}`)
	s.ParseSSEChunk("[DONE]")

	if !s.IsLegalSuccess() {
		t.Error("content + [DONE] 应该是合法成功")
	}
}

// TestSSETermination_ToolCallsDone V1.5: tool_calls + [DONE] = 合法成功
func TestSSETermination_ToolCallsDone(t *testing.T) {
	s := SSETermination{}
	s.ParseSSEChunk(`{"choices":[{"delta":{"tool_calls":[{"id":"c1"}]}}]}`)
	s.ParseSSEChunk("[DONE]")

	if !s.IsLegalSuccess() {
		t.Error("tool_calls + [DONE] 应该是合法成功")
	}
}

// TestSSETermination_EmptyToolCalls V1.6: 空 tool_calls 不算合法终态
func TestSSETermination_EmptyToolCalls(t *testing.T) {
	s := SSETermination{}
	s.ParseSSEChunk(`{"choices":[{"delta":{"tool_calls":[]}}]}`)
	s.ParseSSEChunk("[DONE]")

	if s.IsLegalSuccess() {
		t.Error("空 tool_calls + [DONE] 不应该合法成功")
	}
}

// TestSSETermination_ReasoningOnly V1.5: reasoning-only + [DONE] = 非合法
func TestSSETermination_ReasoningOnly(t *testing.T) {
	s := SSETermination{}
	s.ParseSSEChunk(`{"choices":[{"delta":{"reasoning":"thinking..."}}]}`)
	s.ParseSSEChunk("[DONE]")

	if s.IsLegalSuccess() {
		t.Error("reasoning-only + [DONE] 不应该合法成功")
	}
}

// TestSSETermination_NoDone V1.5: 有 data 但无 [DONE] = 非合法
func TestSSETermination_NoDone(t *testing.T) {
	s := SSETermination{}
	s.ParseSSEChunk(`{"choices":[{"delta":{"content":"hello"}}]}`)

	if s.IsLegalSuccess() {
		t.Error("有 data 但无 [DONE] 不应该合法成功")
	}
}

// TestSSETermination_ReadError V1.5: 断流 = 非合法
func TestSSETermination_ReadError(t *testing.T) {
	s := SSETermination{
		SawContent: true,
		SawDone:    true,
		ReadError:  true,
	}

	if s.IsLegalSuccess() {
		t.Error("断流不应该合法成功")
	}
}

// TestSSETermination_RefusalDone V1.5: refusal + [DONE] = 合法成功
func TestSSETermination_RefusalDone(t *testing.T) {
	s := SSETermination{}
	s.ParseSSEChunk(`{"choices":[{"delta":{"refusal":"I cannot help"}}]}`)
	s.ParseSSEChunk("[DONE]")

	if !s.IsLegalSuccess() {
		t.Error("refusal + [DONE] 应该是合法成功")
	}
}

// TestSSETermination_CorruptedJSON V1.6: 损坏 SSE 不算合法终态
func TestSSETermination_CorruptedJSON(t *testing.T) {
	s := SSETermination{}
	s.ParseSSEChunk(`{broken json`)
	s.ParseSSEChunk("[DONE]")

	if s.IsLegalSuccess() {
		t.Error("损坏 JSON + [DONE] 不应该合法成功")
	}
}

// TestParseNonStreamResponse_Content V1.5: 非流式有 content
func TestParseNonStreamResponse_Content(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"hello world"}}]}`)
	n := ParseNonStreamResponse(body)

	if !n.HasContent || !n.IsLegalSuccess() {
		t.Error("有 content 应该合法成功")
	}
}

// TestParseNonStreamResponse_ToolCalls V1.5: 非流式有 tool_calls
func TestParseNonStreamResponse_ToolCalls(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"c1"}]}}]}`)
	n := ParseNonStreamResponse(body)

	if !n.HasToolCalls || !n.IsLegalSuccess() {
		t.Error("有 tool_calls 应该合法成功")
	}
}

// TestParseNonStreamResponse_EmptyToolCalls V1.6: 非流式空 tool_calls
func TestParseNonStreamResponse_EmptyToolCalls(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"","tool_calls":[]}}]}`)
	n := ParseNonStreamResponse(body)

	if n.IsLegalSuccess() {
		t.Error("空 tool_calls 不应该合法成功")
	}
}

// TestParseNonStreamResponse_Empty V1.5: 非流式空响应
func TestParseNonStreamResponse_Empty(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"","tool_calls":null}}]}`)
	n := ParseNonStreamResponse(body)

	if n.IsLegalSuccess() {
		t.Error("空响应不应该合法成功")
	}
}

// TestIsValidPreferenceModel V1.6: 白名单制
func TestIsValidPreferenceModel(t *testing.T) {
	valid := []string{"deepseek-v4-flash", "deepseek-v4-pro"}
	invalid := []string{"", "deepseek-a4", "qwen3.7-plus", "qwen3.6-plus", "glm-5.2", "unknown-model"}

	for _, m := range valid {
		if !IsValidPreferenceModel(m) {
			t.Errorf("期望 %s 有效", m)
		}
	}
	for _, m := range invalid {
		if IsValidPreferenceModel(m) {
			t.Errorf("期望 %s 无效", m)
		}
	}
}

// --- 密钥测试 V1.6: 调用真实 InitServerSecret() ---

// withEnv 临时设置环境变量并恢复
func withEnv(t *testing.T, env map[string]string, fn func(t *testing.T)) {
	t.Helper()
	orig := map[string]string{}
	for k, v := range env {
		orig[k] = os.Getenv(k)
		os.Setenv(k, v)
	}
	defer func() {
		for k, v := range orig {
			os.Setenv(k, v)
		}
	}()
	fn(t)
}

// TestInitServerSecret_ProductionMissing V1.6: 真实调用 InitServerSecret
func TestInitServerSecret_ProductionMissing(t *testing.T) {
	withEnv(t, map[string]string{
		"APP_ENV":           "production",
		"ATM_SERVER_SECRET": "",
	}, func(t *testing.T) {
		ResetServerSecret()
		err := InitServerSecret()
		if err == nil {
			t.Error("生产环境缺失密钥应该报错")
		}
		if !strings.Contains(err.Error(), "ATM_SERVER_SECRET") {
			t.Errorf("错误信息应该包含 ATM_SERVER_SECRET, got: %v", err)
		}
	})
}

// TestInitServerSecret_ShortSecret V1.6: 短密钥报错
func TestInitServerSecret_ShortSecret(t *testing.T) {
	withEnv(t, map[string]string{
		"APP_ENV":           "production",
		"ATM_SERVER_SECRET": "short",
	}, func(t *testing.T) {
		ResetServerSecret()
		err := InitServerSecret()
		if err == nil {
			t.Error("短密钥应该报错")
		}
		if !strings.Contains(err.Error(), "16") {
			t.Errorf("错误信息应该提到长度不足, got: %v", err)
		}
	})
}

// TestInitServerSecret_ValidSecret V1.6: 合法密钥成功
func TestInitServerSecret_ValidSecret(t *testing.T) {
	validSecret := "this-is-a-valid-production-secret-32chars!"
	withEnv(t, map[string]string{
		"APP_ENV":           "production",
		"ATM_SERVER_SECRET": validSecret,
	}, func(t *testing.T) {
		ResetServerSecret()
		err := InitServerSecret()
		if err != nil {
			t.Errorf("合法密钥应该成功, got: %v", err)
		}
		if !serverSecretInitialized {
			t.Error("应该标记为已初始化")
		}
	})
}

// 确保引入了需要的包
var _ = json.Marshal
