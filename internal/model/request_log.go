package model

import "time"

// RequestLog 请求日志
type RequestLog struct {
	ID          uint      `gorm:"primarykey" json:"id"`
	TokenName   string    `gorm:"size:100" json:"token_name"`
	ChannelName string    `gorm:"size:100" json:"channel_name"`
	Model       string    `gorm:"size:100" json:"model"`
	RoutedModel string    `gorm:"size:100;default:''" json:"routed_model"` // 实际路由到的子模型
	AtmModel    string    `gorm:"size:100;default:''" json:"atm_model"`    // atm模型名称
	StatusCode  int       `json:"status_code"`
	DurationMs  int64     `json:"duration_ms"`
	CreatedAt   time.Time `json:"created_at"`
}

func (RequestLog) TableName() string {
	return "request_logs"
}
