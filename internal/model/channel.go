package model

import (
	"time"

	"gorm.io/gorm"
)

type Channel struct {
	ID            uint           `gorm:"primarykey" json:"id"`
	Name          string         `gorm:"size:100" json:"name"`
	Type          int            `gorm:"default:1" json:"type"` // 1=OpenAI, 2=Anthropic, 3=自定义
	Key           string         `gorm:"size:200" json:"key"`   // API Key
	BaseURL       string         `gorm:"size:500" json:"base_url"`       // 上游 API 地址
	Models        string         `gorm:"size:500" json:"models"`         // 上游大模型，逗号分隔
	ModelMapping  string         `gorm:"type:text" json:"model_mapping"` // 模型映射 JSON
	Status        int            `gorm:"default:1" json:"status"`        // 1=启用，2=禁用
	Priority      int            `gorm:"default:0" json:"priority"`      // 优先级，越高越优先
	Weight        int            `gorm:"default:0" json:"weight"`        // 权重，同优先级内负载均衡
	ModelGroup    string         `gorm:"size:100;index" json:"model_group"` // 聚合组名称，如 "glm-5.2"
	AtmModel      string         `gorm:"size:100;index" json:"atm_model"`   // atm模型名称，如 "deepseek-a4"
	MaxConcurrent int            `gorm:"default:0" json:"max_concurrent"`   // 最大并发数，0=不限
	TestTime      int64          `json:"test_time"`      // 最后测试时间
	ResponseTime  int            `json:"response_time"`  // 响应时间 (ms)
	// Phase 0B: 渠道能力注册表字段
	ContextWindowTokens   int       `gorm:"default:0" json:"context_window_tokens"`   // 上下文窗口总容量（tokens）
	MaxOutputTokens       int       `gorm:"default:0" json:"max_output_tokens"`       // 最大输出 tokens
	SupportsReasoning     bool      `gorm:"default:false" json:"supports_reasoning"`  // 是否支持 reasoning
	Supports1M            bool      `gorm:"default:false" json:"supports_1m"`         // 是否支持 1M 上下文
	CapabilityVerifiedAt  time.Time `json:"capability_verified_at"`                  // 能力验证时间
	CapabilityVersion     string    `gorm:"size:50" json:"capability_version"`        // 能力验证版本
	PricingVersion        string    `gorm:"size:50" json:"pricing_version"`           // 定价版本
	EvidenceReference     string    `gorm:"size:500" json:"evidence_reference"`       // 验证证据引用
	SupportsVision        bool      `gorm:"default:false" json:"supports_vision"`       // 是否支持图片输入
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}

func (c *Channel) TableName() string {
	return "channels"
}
