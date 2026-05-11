package model

import (
	"time"

	"gorm.io/gorm"
)

type Channel struct {
	ID          uint           `gorm:"primarykey" json:"id"`
	Name        string         `gorm:"size:100" json:"name"`
	Type        int            `gorm:"default:1" json:"type"` // 1=OpenAI, 2=Anthropic, 3=自定义
	Key         string         `gorm:"size:200" json:"key"` // API Key
	BaseURL     string         `gorm:"size:500" json:"base_url"` // 上游 API 地址
	Models      string         `gorm:"size:500" json:"models"` // 支持的模型，逗号分隔
	ModelMapping string        `gorm:"type:text" json:"model_mapping"` // 模型映射 JSON
	Status      int            `gorm:"default:1" json:"status"` // 1=启用，2=禁用
	Priority    int            `gorm:"default:0" json:"priority"` // 优先级，越高越优先
	Weight      int            `gorm:"default:0" json:"weight"` // 权重，同优先级内负载均衡
	TestTime    int64          `json:"test_time"` // 最后测试时间
	ResponseTime int           `json:"response_time"` // 响应时间 (ms)
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

func (c *Channel) TableName() string {
	return "channels"
}
