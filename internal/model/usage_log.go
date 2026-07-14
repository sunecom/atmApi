package model

import (
	"strings"
	"time"
)

// UsageLog 每次调用的 token 用量日志
// 用于精确统计每笔调用的成本（input_tokens * 输入单价 + output_tokens * 输出单价）
type UsageLog struct {
	RequestedModel        string    `gorm:"size:100;index" json:"requested_model"`
	ActualModel           string    `gorm:"size:150;index" json:"actual_model"`
	UpstreamProvider      string    `gorm:"size:100;index" json:"upstream_provider"`
	PolicyVersion         string    `gorm:"size:50;index" json:"policy_version"`
	CacheWriteTokens      int64     `json:"cache_write_tokens"`
	ReasoningTokens       int64     `json:"reasoning_tokens"`
	VisibleOutputTokens   int64     `json:"visible_output_tokens"`
	CompletionTokens      int64     `json:"completion_tokens"`
	UpstreamReportedCost  float64   `json:"upstream_reported_cost"`
	UpstreamCostCurrency  string    `gorm:"size:32" json:"upstream_cost_currency"`
	CostAmount            float64   `json:"cost_amount"`
	CostCurrency          string    `gorm:"size:32" json:"cost_currency"`
	CostSource            string    `gorm:"size:32;index" json:"cost_source"`
	PricingSnapshotID     string    `gorm:"size:100;index" json:"pricing_snapshot_id"`
	LocalResponseCacheHit bool      `gorm:"index" json:"local_response_cache_hit"`
	SingleflightShared    bool      `gorm:"index" json:"singleflight_shared"`
	SessionIDHashPrefix   string    `gorm:"size:24;index" json:"session_id_hash_prefix"`
	TerminalState         string    `gorm:"size:50;index" json:"terminal_state"`
	FinishReason          string    `gorm:"size:50" json:"finish_reason"`
	RetryCount            int       `json:"retry_count"`
	BreakerState          string    `gorm:"size:24" json:"breaker_state"`
	TTFTMs                int64     `json:"ttft_ms"`
	PreFirstByteFailure   bool      `json:"pre_first_byte_failure"`
	StreamInterrupted     bool      `json:"stream_interrupted"`
	ID                    uint      `gorm:"primarykey" json:"id"`
	TokenID               uint      `gorm:"index:idx_usage_token_time" json:"token_id"` // 关联 token
	TokenName             string    `gorm:"size:100" json:"token_name"`                 // token 名称（冗余，方便查）
	PlanName              string    `gorm:"size:50" json:"plan_name"`                   // 套餐名（basic/standard/premium/pro/weekly）
	ChannelID             uint      `gorm:"index" json:"channel_id"`                    // 关联渠道
	ChannelName           string    `gorm:"size:100" json:"channel_name"`               // 渠道名称（冗余，方便查）
	Model                 string    `gorm:"size:100" json:"model"`                      // 实际调用模型名
	InputTokens           int64     `json:"input_tokens"`                               // 输入 token 数
	OutputTokens          int64     `json:"output_tokens"`                              // 输出 token 数
	CachedTokens          int64     `json:"cached_tokens"`                              // 缓存命中的 token 数
	TotalTokens           int64     `json:"total_tokens"`                               // 总计 token 数
	EstimatedCost         float64   `json:"estimated_cost"`                             // 估算成本（元）
	StatusCode            int       `json:"status_code"`                                // HTTP 状态码
	DurationMs            int64     `json:"duration_ms"`                                // 耗时（毫秒）
	CreatedAt             time.Time `json:"created_at"`
}

func (UsageLog) TableName() string {
	return "usage_logs"
}

// 模型定价配置（元/千 token）
// 渠道模型定价（输入/输出），单位：元/千token
var ModelPricingMap = map[string]struct {
	InputPrice  float64 // 输入单价（元/千token）
	OutputPrice float64 // 输出单价（元/千token）
}{
	// ===== 通义千问系列 =====
	"qwen3.7-plus":  {InputPrice: 0.005, OutputPrice: 0.02}, // qwen 官方定价
	"qwen3.5-plus":  {InputPrice: 0.005, OutputPrice: 0.02},
	"qwen-turbo":    {InputPrice: 0.002, OutputPrice: 0.008},
	"qwen2.5-72b":   {InputPrice: 0.008, OutputPrice: 0.024},
	"qwen2.5-coder": {InputPrice: 0.003, OutputPrice: 0.012},
	"qwen2.5-14b":   {InputPrice: 0.003, OutputPrice: 0.012},

	// ===== DeepSeek 系列 =====
	"deepseek-v4-flash": {InputPrice: 0.002, OutputPrice: 0.008},
	"deepseek-chat":     {InputPrice: 0.002, OutputPrice: 0.008},
	"deepseek-reasoner": {InputPrice: 0.004, OutputPrice: 0.016},

	// ===== GLM-5.2（词元/中转）=====
	"glm-5.2":      {InputPrice: 0.008, OutputPrice: 0.028},
	"glm-5.2-team": {InputPrice: 0.008, OutputPrice: 0.028},
	"glm-4":        {InputPrice: 0.005, OutputPrice: 0.02},

	// ===== 默认兜底 =====
	"default": {InputPrice: 0.01, OutputPrice: 0.03},
}

// GetModelPrice 获取模型单价
// 如果不在定价表里，返回默认值
func GetModelPrice(modelName string) (inputPrice, outputPrice float64) {
	if p, ok := ModelPricingMap[modelName]; ok {
		return p.InputPrice, p.OutputPrice
	}
	// 模糊匹配：提取前缀
	for key, p := range ModelPricingMap {
		if len(modelName) >= len(key) && modelName[:len(key)] == key {
			return p.InputPrice, p.OutputPrice
		}
	}
	// 默认
	return ModelPricingMap["default"].InputPrice, ModelPricingMap["default"].OutputPrice
}

// CalculateCost 计算某次调用的成本（元）
// cachedTokens: 缓存命中的 token 数（0 表示不区分缓存）
func CalculateCost(inputTokens, outputTokens, cachedTokens int64, modelName string) float64 {
	inputPrice, outputPrice := GetModelPrice(modelName)
	if strings.Contains(strings.ToLower(modelName), "glm-5.2") {
		// Provider cache pricing is not a universal 10% of input. Historical
		// GLM-5.2 summaries use the conservative full input price; new rows use
		// their immutable ProviderPriceSnapshot or upstream-reported cost.
		return float64(inputTokens)/1000*0.008 + float64(outputTokens)/1000*0.028
	}
	// 缓存命中的 token 按 10% 计费
	uncachedTokens := inputTokens - cachedTokens
	if uncachedTokens < 0 {
		uncachedTokens = 0
	}
	cost := float64(uncachedTokens)/1000*inputPrice + float64(cachedTokens)/1000*inputPrice*0.1 + float64(outputTokens)/1000*outputPrice
	return cost
}
