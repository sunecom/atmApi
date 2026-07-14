package model

import (
	"time"
)

// GLMPointsLedger GLM-5.2 客户点数账本
type GLMPointsLedger struct {
	ID                    uint      `gorm:"primaryKey" json:"id"`
	TokenID               uint      `gorm:"not null;index:idx_token_period,unique" json:"token_id"`
	PlanName              string    `gorm:"not null;size:50" json:"plan_name"`
	PeriodStart           time.Time `gorm:"not null;index:idx_token_period,unique" json:"period_start"`
	PeriodEnd             time.Time `gorm:"not null" json:"period_end"`
	TotalPoints           int       `gorm:"not null" json:"total_points"`
	UsedPoints            int       `gorm:"default:0" json:"used_points"`
	FiveHourPoints        int       `gorm:"not null" json:"five_hour_points"`
	FiveHourUsed          int       `gorm:"default:0" json:"five_hour_used"`
	DailyPoints           int       `gorm:"not null" json:"daily_points"`
	DailyUsed             int       `gorm:"default:0" json:"daily_used"`
	StandardPricePerPoint float64   `gorm:"default:0.01;type:decimal(10,6)" json:"standard_price_per_point"`
	CreatedAt             time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt             time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (GLMPointsLedger) TableName() string {
	return "glm_points_ledger"
}
