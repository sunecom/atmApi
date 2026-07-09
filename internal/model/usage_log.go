package model

import "time"

// UsageLog 每次调用的 token 用量日志
// 用于精确统计每笔调用的成本（input_tokens * 输入单价 + output_tokens * 输出单价）
type UsageLog struct {
	ID             uint      `gorm:"primarykey" json:"id"`
	TokenID        uint      `gorm:"index:idx_usage_token_time" json:"token_id"`       // 关联 token
	TokenName      string    `gorm:"size:100" json:"token_name"`                        // token 名称（冗余，方便查）
	PlanName       string    `gorm:"size:50" json:"plan_name"`                          // 套餐名（basic/standard/premium/pro/weekly）
	ChannelID      uint      `gorm:"index" json:"channel_id"`                           // 关联渠道
	ChannelName    string    `gorm:"size:100" json:"channel_name"`                      // 渠道名称（冗余，方便查）
	Model          string    `gorm:"size:100" json:"model"`                             // 实际调用模型名
	InputTokens    int64     `json:"input_tokens"`                                      // 输入 token 数
	OutputTokens   int64     `json:"output_tokens"`                                     // 输出 token 数
	CachedTokens   int64     `json:"cached_tokens"`                                     // 缓存命中的 token 数
	TotalTokens    int64     `json:"total_tokens"`                                      // 总计 token 数
	EstimatedCost  float64   `json:"estimated_cost"`                                    // 估算成本（元）
	StatusCode     int       `json:"status_code"`                                       // HTTP 状态码
	DurationMs     int64     `json:"duration_ms"`                                       // 耗时（毫秒）
	CreatedAt      time.Time `json:"created_at"`
}

func (UsageLog) TableName() string {
	return "usage_logs"
}



// CalculateCost 计算某次调用的成本（元）
// cachedTokens: 缓存命中的 token 数（0 表示不区分缓存）
func CalculateCost(inputTokens, outputTokens, cachedTokens int64, modelName string) float64 {
	return CalculateCostDB(inputTokens, outputTokens, cachedTokens, modelName)
}
