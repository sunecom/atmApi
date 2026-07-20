package model

import "time"

// Plan 套餐配置表
// 把原来硬编码在代码里的套餐配额搬到数据库，方便管理
type Plan struct {
	ID           uint      `gorm:"primarykey" json:"id"`
	Name         string    `gorm:"uniqueIndex;size:50" json:"name"`           // 套餐标识：basic/pro/flagship/starter/advanced/enterprise
	DisplayName  string    `gorm:"size:100" json:"display_name"`              // 显示名称
	Hourly5Max   int64     `gorm:"column:hourly_5_max;default:0" json:"hourly_5_max"`  // 5小时配额，0=不限
	DailyMax     int64     `gorm:"default:0" json:"daily_max"`                // 每日配额，0=不限
	WeeklyMax    int64     `gorm:"default:0" json:"weekly_max"`               // 每周配额，0=不限
	MonthlyMax   int64     `gorm:"default:0" json:"monthly_max"`              // 每月配额，0=不限
	MaxQPS       int64     `gorm:"default:0" json:"max_qps"`                  // 最大并发QPS，0=不限
	MaxRPM       int64     `gorm:"default:0" json:"max_rpm"`                  // 每分钟请求数，0=不限
	MaxChannelRPM int64    `gorm:"default:0" json:"max_channel_rpm"`          // 单渠道每分钟请求数，0=不限
	MaxOutputTokens int   `gorm:"default:0" json:"max_output_tokens"`         // 单次最大输出 token，0=不限
	MaxInputTokens  int   `gorm:"default:0" json:"max_input_tokens"`          // 单次最大输入 token，0=不限
	Monthly1MMax    int   `gorm:"column:monthly_1m_max;default:0" json:"monthly_1m_max"`   // 1M大输入次数上限，0=不限
	DailyImageMax   int64 `gorm:"default:0" json:"daily_image_max"`           // 每日图片次数限制，0=不限
	ImageEnabled    bool  `gorm:"default:false" json:"image_enabled"`         // 是否启用图片功能
	AllowedModels   string `gorm:"type:text" json:"allowed_models"`           // 允许的模型列表，JSON数组
	Description  string    `gorm:"type:text" json:"description"`              // 套餐说明
	Price        string    `gorm:"size:20" json:"price"`                      // 价格（展示用）
	SkipHourly   bool      `gorm:"default:false" json:"skip_hourly"`          // 是否跳过5小时限流
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (p *Plan) TableName() string {
	return "plans"
}
