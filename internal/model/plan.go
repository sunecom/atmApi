package model

import "time"

// Plan 套餐配置表
// 把原来硬编码在代码里的套餐配额搬到数据库，方便管理
type Plan struct {
	ID           uint      `gorm:"primarykey" json:"id"`
	Name         string    `gorm:"uniqueIndex;size:50" json:"name"`           // 套餐标识：basic/standard/premium/pro/weekly
	DisplayName  string    `gorm:"size:100" json:"display_name"`              // 显示名称：性价比月卡
	Hourly5Max   int64     `gorm:"column:hourly_5_max;default:0" json:"hourly_5_max"`             // 5小时配额，0=不限
	WeeklyMax    int64     `gorm:"default:0" json:"weekly_max"`               // 每周配额，0=不限
	Description  string    `gorm:"type:text" json:"description"`              // 套餐说明
	Price        string    `gorm:"size:20" json:"price"`                      // 价格（展示用）
	SkipHourly   bool      `gorm:"default:false" json:"skip_hourly"`          // 是否跳过5小时限流
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (p *Plan) TableName() string {
	return "plans"
}
