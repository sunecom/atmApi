package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
)

// SessionContext 会话上下文（不含原始 Token 和会话 ID）
type SessionContext struct {
	SessionHash    string // HMAC(token_id + session_id)，不可逆
	TokenID        uint
	SessionMissing bool   // 是否缺少会话 ID
	RawSessionID   string // 仅用于本次请求，不持久化
}

// P0-6 V1.4: 密钥初始化返回 error，不用 log.Fatal
var devSecretOnce sync.Once
var devSecret []byte
var serverSecret []byte
var serverSecretInitialized bool

// InitServerSecret 启动时一次性初始化密钥
// 返回 error 由 main() 决定如何处理
func InitServerSecret() error {
	secret := os.Getenv("ATM_SERVER_SECRET")
	if secret != "" {
		if len(secret) < 16 {
			return fmt.Errorf("ATM_SERVER_SECRET 长度不足 16 字符")
		}
		serverSecret = []byte(secret)
		serverSecretInitialized = true
		log.Printf("[会话] ✅ ATM_SERVER_SECRET 已加载 (%d 字符)", len(secret))
		return nil
	}

	appEnv := os.Getenv("APP_ENV")
	if strings.ToLower(appEnv) == "development" || strings.ToLower(appEnv) == "dev" {
		var initErr error
		devSecretOnce.Do(func() {
			devSecret = make([]byte, 32)
			if _, err := rand.Read(devSecret); err != nil {
				initErr = fmt.Errorf("开发环境密钥生成失败: %w", err)
				return
			}
			log.Printf("[会话] ⚠️ 开发环境：已生成随机临时密钥")
		})
		if initErr != nil {
			return initErr
		}
		serverSecret = devSecret
		serverSecretInitialized = true
		return nil
	}

	return fmt.Errorf("ATM_SERVER_SECRET 未设置，请设置该环境变量或 APP_ENV=development")
}

// getServerSecret 返回已初始化的密钥
// V1.5: 未初始化时 panic（编程错误，不应到达此处）
func getServerSecret() []byte {
	if !serverSecretInitialized || len(serverSecret) == 0 {
		panic("getServerSecret called before InitServerSecret")
	}
	return serverSecret
}

// ResolveSession 从 HTTP 请求头解析会话上下文
// P0-1 修复：使用 http.Header.Get() 而非手动 map 查找（大小写不敏感）
// 优先级：X-Atm-Session-Id > X-Session-Id > X-Conversation-Id
func ResolveSession(header http.Header, tokenID uint) *SessionContext {
	sessionID := ""
	for _, key := range []string{"X-Atm-Session-Id", "X-Session-Id", "X-Conversation-Id"} {
		if val := header.Get(key); val != "" {
			sessionID = val
			break
		}
	}

	ctx := &SessionContext{
		TokenID: tokenID,
	}

	if sessionID == "" {
		ctx.SessionMissing = true
		// P0-2 修复：缺失 session 时使用特殊标记，禁用粘性缓存
		ctx.SessionHash = fmt.Sprintf("no_session_%d", tokenID)
		return ctx
	}

	// HMAC(token_id + session_id)
	mac := hmac.New(sha256.New, getServerSecret())
	mac.Write([]byte(fmt.Sprintf("%d:%s", tokenID, sessionID)))
	ctx.SessionHash = hex.EncodeToString(mac.Sum(nil))[:32] // 32 hex = 128 bit
	ctx.RawSessionID = sessionID

	return ctx
}

// ContextLogEntry 上下文决策日志（不含正文）
type ContextLogEntry struct {
	SessionHash        string `json:"session_hash"`
	TokenID            uint   `json:"token_id"`
	SessionIDMissing   bool   `json:"session_id_missing"`
	OriginalMessages   int    `json:"original_messages"`
	FinalMessages      int    `json:"final_messages"`
	EstimatedTokens    int    `json:"estimated_tokens"`
	ContextMode        string `json:"context_mode"`
	WouldDropGroups    int    `json:"would_drop_groups"`
	WouldSummarize     bool   `json:"would_summarize"`
	SelectedModel      string `json:"selected_model"`
	PreviousModel      string `json:"previous_model,omitempty"`
	SwitchReason       string `json:"switch_reason,omitempty"`
	ToolTransactionAct bool   `json:"tool_transaction_active"`
}

// LogContextDecision 记录上下文决策（不含正文）
func LogContextDecision(entry ContextLogEntry) {
	data, _ := json.Marshal(entry)
	log.Printf("[上下文决策] %s", string(data))
}

// PreferenceCacheKey 生成会话级 Preference Cache 键
// 如果 session 缺失，返回空字符串表示禁用缓存
func PreferenceCacheKey(sessionHash string) string {
	if strings.HasPrefix(sessionHash, "no_session_") {
		return "" // 禁用缓存
	}
	return "pref:" + sessionHash
}

// IsSessionMissing 检查是否缺少 session ID
func IsSessionMissing(sessionHash string) bool {
	return strings.HasPrefix(sessionHash, "no_session_")
}

// HasActiveToolTransaction 检查当前是否在工具事务中
// P0-5 V1.2 修复：柯大侠指出旧代码把 tool 响应视为事务结束，
// 但在 Chat Completions 协议中，带 tool 结果的请求还需要模型读取并生成回答，
// 此时仍必须保持原模型。
//
// 状态机规则：
// 1. assistant(tool_calls) → tool 结果 → 事务仍活跃（等待 assistant 完成）
// 2. assistant(tool_calls) 后无 tool 结果 → 事务活跃
// 3. 不含 tool_calls 的 assistant 回答 → 事务完成
// 4. 新真实 user 请求 → 事务结束，转入会话粘性
func HasActiveToolTransaction(messages []map[string]interface{}) bool {
	if len(messages) == 0 {
		return false
	}

	// 从后往前扫描
	for i := len(messages) - 1; i >= 0; i-- {
		role, _ := messages[i]["role"].(string)

		switch role {
		case "tool":
			// tool 结果 → 事务仍活跃，等待 assistant 完成
			return true
		case "assistant":
			if _, ok := messages[i]["tool_calls"]; ok {
				// assistant 发起了 tool_calls → 事务活跃
				// 无论后面有没有 tool 响应，都是事务活跃
				return true
			}
			// 普通 assistant 消息（无 tool_calls）→ 事务完成
			return false
		case "user":
			// P0-5: 检查是否是 OpenClaw 伪 user 工具结果
			// OpenClaw 以 role=user 注入工具结果，通常以 [tool result] 或类似格式开头
			content := getUserText(messages[i])
			if isOpenClawToolResult(content) {
				return true
			}
			// 真实用户新问题 → 事务结束
			return false
		}
	}
	return false
}

// isOpenClawToolResult 检查是否是 OpenClaw 伪 user 工具结果
// OpenClaw 工具结果特征：通常包含特定标记
func isOpenClawToolResult(content string) bool {
	if content == "" {
		return false
	}
	// OpenClaw 工具结果通常有这些标记
	markers := []string{
		"[tool result",
		"[tool_result",
		"[Tool Result",
		"Tool result:",
		"--- Tool Result",
		"--- Tool Output",
	}
	for _, marker := range markers {
		if strings.HasPrefix(strings.TrimSpace(content), marker) {
			return true
		}
	}
	// 另一种模式：非常大的 user 消息（>2000 字节），前面有 assistant(tool_calls)
	// 这种情况下难以准确判断，先不做启发式检测
	return false
}
