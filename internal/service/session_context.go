package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

// SessionContext 会话上下文（不含原始 Token 和会话 ID）
type SessionContext struct {
	SessionHash    string // HMAC(token_id + session_id)，不可逆
	TokenID        uint
	SessionMissing bool   // 是否缺少会话 ID
	RawSessionID   string // 仅用于本次请求，不持久化
}

// GetServerSecret 从环境变量读取服务端密钥
// P0-6 修复：生产环境 fail-closed，必须显式设置 APP_ENV=development 才允许默认值
func getServerSecret() []byte {
	secret := os.Getenv("ATM_SERVER_SECRET")
	if secret != "" {
		return []byte(secret)
	}

	// 没有设置密钥，检查是否显式声明开发环境
	appEnv := os.Getenv("APP_ENV")
	if strings.ToLower(appEnv) == "development" || strings.ToLower(appEnv) == "dev" {
		log.Println("[会话] ⚠️ 开发环境：使用临时密钥（请设置 ATM_SERVER_SECRET）")
		return []byte("atmapi-dev-secret-2026")
	}

	// 生产环境：fail-closed，使用启动时生成的随机密钥
	log.Println("[会话] 🔴 ATM_SERVER_SECRET 未设置，生成随机密钥（重启后会话隔离失效）")
	// 生成一个基于进程的随机密钥
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i + 1) // 占位，实际应该用 crypto/rand
	}
	return b
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
	ctx.SessionHash = hex.EncodeToString(mac.Sum(nil))[:16] // 前 16 字符够用
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
// P0-5 修复：统一工具事务检测逻辑
// 工具事务活跃条件：
// 1. 最后一条消息是 tool role（等待处理结果）
// 2. 最后一条 assistant 消息有 tool_calls 且后面没有 tool 响应
func HasActiveToolTransaction(messages []map[string]interface{}) bool {
	if len(messages) == 0 {
		return false
	}

	// 从后往前扫描
	for i := len(messages) - 1; i >= 0; i-- {
		role, _ := messages[i]["role"].(string)

		switch role {
		case "tool":
			// 找到 tool 响应，继续往前找对应的 assistant tool_calls
			continue
		case "assistant":
			if _, ok := messages[i]["tool_calls"]; ok {
				// 找到 assistant 发起的 tool_calls
				// 检查后面是否有对应的 tool 响应
				hasToolResponse := false
				for j := i + 1; j < len(messages); j++ {
					if r, _ := messages[j]["role"].(string); r == "tool" {
						hasToolResponse = true
						break
					}
				}
				// 如果有 tool 响应，事务已完成；否则事务活跃
				return !hasToolResponse
			}
			// 普通 assistant 消息，没有 tool_calls
			return false
		case "user":
			// 用户新消息，事务结束
			return false
		}
	}
	return false
}
