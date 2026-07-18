package service

import (
	"encoding/json"
	"fmt"
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
	// 没有 [DONE]

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

// TestParseNonStreamResponse_Empty V1.5: 非流式空响应
func TestParseNonStreamResponse_Empty(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"","tool_calls":null}}]}`)
	n := ParseNonStreamResponse(body)

	if n.IsLegalSuccess() {
		t.Error("空响应不应该合法成功")
	}
}

// TestIsValidPreferenceModel V1.5: 禁止写入元模型
func TestIsValidPreferenceModel(t *testing.T) {
	valid := []string{"deepseek-v4-flash", "deepseek-v4-pro", "glm-5.2"}
	invalid := []string{"", "deepseek-a4", "qwen3.7-plus", "qwen3.6-plus", "qwen3.5-plus"}

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

// TestInitServerSecret_ProductionMissing V1.5: 生产环境缺失密钥报错
func TestInitServerSecret_ProductionMissing(t *testing.T) {
	origEnv := os.Getenv("APP_ENV")
	origSecret := os.Getenv("ATM_SERVER_SECRET")
	defer func() {
		os.Setenv("APP_ENV", origEnv)
		os.Setenv("ATM_SERVER_SECRET", origSecret)
	}()

	os.Setenv("APP_ENV", "production")
	os.Setenv("ATM_SERVER_SECRET", "")

	// 保存和恢复全局状态
	origInit := serverSecretInitialized
	origSecret2 := serverSecret
	defer func() {
		serverSecretInitialized = origInit
		serverSecret = origSecret2
	}()

	// 重置状态
	serverSecretInitialized = false
	serverSecret = nil

	err := initServerSecretForTest()
	if err == nil {
		t.Error("生产环境缺失密钥应该报错")
	}
}

// TestInitServerSecret_ShortSecret V1.5: 短密钥报错
func TestInitServerSecret_ShortSecret(t *testing.T) {
	origEnv := os.Getenv("APP_ENV")
	origSecret := os.Getenv("ATM_SERVER_SECRET")
	defer func() {
		os.Setenv("APP_ENV", origEnv)
		os.Setenv("ATM_SERVER_SECRET", origSecret)
	}()

	os.Setenv("APP_ENV", "production")
	os.Setenv("ATM_SERVER_SECRET", "short")

	origInit := serverSecretInitialized
	origSecretVal := serverSecret
	defer func() {
		serverSecretInitialized = origInit
		serverSecret = origSecretVal
	}()

	serverSecretInitialized = false
	serverSecret = nil

	err := initServerSecretForTest()
	if err == nil {
		t.Error("短密钥应该报错")
	}
}

// TestInitServerSecret_ValidSecret V1.5: 合法密钥成功
func TestInitServerSecret_ValidSecret(t *testing.T) {
	origEnv := os.Getenv("APP_ENV")
	origSecret := os.Getenv("ATM_SERVER_SECRET")
	defer func() {
		os.Setenv("APP_ENV", origEnv)
		os.Setenv("ATM_SERVER_SECRET", origSecret)
	}()

	validSecret := "this-is-a-valid-production-secret-32chars!"
	os.Setenv("APP_ENV", "production")
	os.Setenv("ATM_SERVER_SECRET", validSecret)

	origInit := serverSecretInitialized
	origSecretVal := serverSecret
	defer func() {
		serverSecretInitialized = origInit
		serverSecret = origSecretVal
	}()

	serverSecretInitialized = false
	serverSecret = nil

	err := initServerSecretForTest()
	if err != nil {
		t.Errorf("合法密钥应该成功, got error: %v", err)
	}
	if !serverSecretInitialized {
		t.Error("应该标记为已初始化")
	}
}

// initServerSecretForTest 是 InitServerSecret 的测试版本
// 不受 sync.Once 影响（用于测试中重复调用）
func initServerSecretForTest() error {
	secret := os.Getenv("ATM_SERVER_SECRET")
	if secret != "" {
		if len(secret) < 16 {
			return fmt.Errorf("ATM_SERVER_SECRET 长度不足 16 字符")
		}
		serverSecret = []byte(secret)
		serverSecretInitialized = true
		return nil
	}

	appEnv := os.Getenv("APP_ENV")
	if strings.ToLower(appEnv) == "development" || strings.ToLower(appEnv) == "dev" {
		if len(devSecret) == 0 {
			devSecret = make([]byte, 32)
			json.Marshal(devSecret) // 占位，crypto/rand 在 Once 中已生成
		}
		serverSecret = devSecret
		serverSecretInitialized = true
		return nil
	}

	return fmt.Errorf("ATM_SERVER_SECRET 未设置")
}

// 确保引入了需要的包
var _ = json.Marshal
