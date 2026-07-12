package model

import "time"

// PromptProfile 请求结构特征（不存内容，只存量化特征）
type PromptProfile struct {
	ID            uint      `gorm:"primarykey" json:"id"`
	TokenID       uint      `gorm:"index:idx_token_time" json:"token_id"`
	TokenName     string    `gorm:"size:100;index" json:"token_name"`
	Model         string    `gorm:"size:100" json:"model"`
	MsgCount      int       `gorm:"default:0" json:"msg_count"`               // messages 总数
	SystemLen     int       `gorm:"default:0" json:"system_len"`              // system 指令长度
	HistoryLen    int       `gorm:"default:0" json:"history_len"`             // 历史对话长度
	NewMsgLen     int       `gorm:"default:0" json:"new_msg_len"`             // 最新消息长度
	SystemRatio   float64   `gorm:"default:0" json:"system_ratio"`            // system 占比 (0-1)
	HistoryRounds int       `gorm:"default:0" json:"history_rounds"`          // 对话轮数
	FirstRole     string    `gorm:"size:20;default:''" json:"first_role"`     // 消息1的角色(system/user)
	HasImage      bool      `gorm:"default:false" json:"has_image"`           // 是否含图片
	HasToolCalls  bool      `gorm:"default:false" json:"has_tool_calls"`      // 是否含工具调用
	PrefixHash    string    `gorm:"size:64;index" json:"prefix_hash"`         // 结构指纹（system+前2轮hash）
	CacheScore    int       `gorm:"default:0" json:"cache_score"`             // 缓存友好度评分 0-100
	InputTokens   int64     `gorm:"default:0" json:"input_tokens"`            // 实际输入 token（从usage_log匹配）
	CachedTokens  int64     `gorm:"default:0" json:"cached_tokens"`           // 缓存命中 token
	CreatedAt     time.Time `json:"created_at"`
}

func (PromptProfile) TableName() string {
	return "prompt_profiles"
}
