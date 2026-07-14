package model

import (
	"strings"
	"time"
)

// ModelPricing 模型定价配置（数据库化）
// 支持按量计费和包月两种模式
type ModelPricing struct {
	InputCacheWritePrice float64   `gorm:"default:0" json:"input_cache_write_price"`
	ReasoningPrice       float64   `gorm:"default:0" json:"reasoning_price"`
	Currency             string    `gorm:"size:16;default:CNY" json:"currency"`
	SnapshotID           string    `gorm:"size:100;index" json:"snapshot_id"`
	ID                   uint      `gorm:"primarykey" json:"id"`
	ModelName            string    `gorm:"size:100;uniqueIndex" json:"model_name"`          // 模型名（如 deepseek-v4-flash）
	PricingType          string    `gorm:"size:20;default:pay_per_use" json:"pricing_type"` // pay_per_use / monthly
	MonthlyFee           float64   `gorm:"default:0" json:"monthly_fee"`                    // 包月费用（元）
	IncludedQuota        int64     `gorm:"default:0" json:"included_quota"`                 // 包月包含额度（token），0=无限
	InputPrice           float64   `gorm:"default:0" json:"input_price"`                    // 输入价（元/千token），缓存未命中
	InputCachePrice      float64   `gorm:"default:0" json:"input_cache_price"`              // 缓存命中输入价
	OutputPrice          float64   `gorm:"default:0" json:"output_price"`                   // 输出价
	OveragePrice         float64   `gorm:"default:0" json:"overage_price"`                  // 超额单价
	Provider             string    `gorm:"size:50" json:"provider"`                         // 供应商（deepseek/qwen/zhipu）
	EffectiveDate        time.Time `json:"effective_date"`                                  // 生效日期
	Status               int       `gorm:"default:1" json:"status"`                         // 1=启用 0=停用
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// ProviderSnapshot converts persisted per-1K prices into an immutable
// per-million provider snapshot.
func (p ModelPricing) ProviderSnapshot() ProviderPriceSnapshot {
	cached := p.InputCachePrice
	if cached == 0 {
		cached = p.InputPrice
	}
	cacheWrite := p.InputCacheWritePrice
	if cacheWrite == 0 {
		cacheWrite = p.InputPrice
	}
	reasoning := p.ReasoningPrice
	if reasoning == 0 {
		reasoning = p.OutputPrice
	}
	currency := p.Currency
	if currency == "" {
		currency = "CNY"
	}
	snapshotID := p.SnapshotID
	if snapshotID == "" {
		snapshotID = "model-pricing-record"
	}
	return ProviderPriceSnapshot{ID: snapshotID, Provider: p.Provider, Model: p.ModelName,
		Currency: currency, InputPricePerMillion: p.InputPrice * 1000,
		CachedPricePerMillion: cached * 1000, CacheWritePricePerMillion: cacheWrite * 1000,
		OutputPricePerMillion: p.OutputPrice * 1000, ReasoningPricePerMillion: reasoning * 1000}
}

func (ModelPricing) TableName() string {
	return "model_pricings"
}

// GetPricingByModel 根据模型名获取定价
func GetPricingByModel(modelName string) (*ModelPricing, bool) {
	var pricing ModelPricing
	err := DB.Where("model_name = ? AND status = 1", modelName).First(&pricing).Error
	if err != nil {
		return nil, false
	}
	return &pricing, true
}

// CalculateCostDB 计算某次调用的成本（元）- 从数据库读取定价
// inputTokens: 输入 token 数
// outputTokens: 输出 token 数
// cachedTokens: 缓存命中的 token 数
// modelName: 模型名
func CalculateCostDB(inputTokens, outputTokens, cachedTokens int64, modelName string) float64 {
	pricing, ok := GetPricingByModel(modelName)
	if !ok {
		// 未找到定价，返回 0
		return 0
	}

	if pricing.PricingType == "monthly" {
		// 包月模型：额度内成本为 0
		// 超额部分按 overage_price 计算（这里简化处理，实际应该按月统计）
		return 0
	}

	// 按量计费
	// 缓存命中部分
	cachePrice := pricing.InputCachePrice
	if cachePrice == 0 && strings.Contains(strings.ToLower(modelName), "glm-5.2") {
		cachePrice = pricing.InputPrice
	}
	cacheCost := float64(cachedTokens) / 1000 * cachePrice
	// 缓存未命中部分
	uncachedTokens := inputTokens - cachedTokens
	if uncachedTokens < 0 {
		uncachedTokens = 0
	}
	uncachedCost := float64(uncachedTokens) / 1000 * pricing.InputPrice
	// 输出成本
	outputCost := float64(outputTokens) / 1000 * pricing.OutputPrice

	return cacheCost + uncachedCost + outputCost
}
