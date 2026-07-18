package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
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
func getServerSecret() []byte {
	secret := os.Getenv("ATM_SERVER_SECRET")
	if secret == "" {
		// 开发环境默认值
		log.Println("[会话] ⚠️ 未设置 ATM_SERVER_SECRET，使用开发默认值")
		secret = "atmapi-dev-secret-2026"
	}
	return []byte(secret)
}

// ResolveSession 从请求头解析会话上下文
// 优先级：X-Atm-Session-ID > X-Session-ID > X-Conversation-ID
func ResolveSession(headers map[string]string, tokenID uint) *SessionContext {
	sessionID := ""
	for _, key := range []string{"X-Atm-Session-ID", "X-Session-ID", "X-Conversation-ID"} {
		if val, ok := headers[key]; ok && val != "" {
			sessionID = val
			break
		}
		// 也检查小写
		lowerKey := strings.ToLower(key)
		if val, ok := headers[lowerKey]; ok && val != "" {
			sessionID = val
			break
		}
	}

	ctx := &SessionContext{
		TokenID: tokenID,
	}

	if sessionID == "" {
		ctx.SessionMissing = true
		ctx.SessionHash = fmt.Sprintf("anon_%d", tokenID)
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
func PreferenceCacheKey(sessionHash string) string {
	return "pref:" + sessionHash
}

// HasActiveToolTransaction 检查最后一条消息是否在工具事务中
func HasActiveToolTransaction(messages []map[string]interface{}) bool {
	if len(messages) == 0 {
		return false
	}
	// 从后往前找最后一条 assistant 或 tool 消息
	for i := len(messages) - 1; i >= 0; i-- {
		role, _ := messages[i]["role"].(string)
		if role == "tool" {
			return true
		}
		if role == "assistant" {
			if _, ok := messages[i]["tool_calls"]; ok {
				return true
			}
			return false
		}
	}
	return false
}
