package model

import "time"

// RateLimit 滑动窗口限流记录
type RateLimit struct {
	ID          uint      `gorm:"primarykey" json:"id"`
	TokenID     uint      `gorm:"index:idx_token_time" json:"token_id"`      // 关联 token
	RequestTime int64     `gorm:"index:idx_token_time" json:"request_time"`  // 请求时间戳（Unix秒）
	CreatedAt   time.Time `json:"created_at"`
}

func (r *RateLimit) TableName() string {
	return "rate_limits"
}
