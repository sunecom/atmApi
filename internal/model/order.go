package model

import (
	"time"

	"gorm.io/gorm"
)

// Order 订单模型（持久化）
type Order struct {
	ID              uint           `gorm:"primarykey" json:"id"`
	OrderNo         string         `gorm:"uniqueIndex;size:50" json:"order_no"`                     // 商户订单号
	UserOpenID      string         `gorm:"size:64" json:"user_open_id"`                             // 用户 open_id
	PlanName        string         `gorm:"size:50" json:"plan_name"`                                // 购买的套餐名
	Price           float64        `gorm:"default:0" json:"price"`                                  // 金额（元）
	Status          string         `gorm:"size:20;default:pending" json:"status"`                   // pending/paid/cancelled/expired/refunded
	AlipayTradeNo   string         `gorm:"size:64;default:''" json:"alipay_trade_no"`               // 支付宝交易号
	TokenName       string         `gorm:"size:100;default:''" json:"token_name"`                   // 支付成功后创建的 Token 名
	PayURL          string         `gorm:"type:text;default:''" json:"pay_url"`                      // 支付链接
	PaidAt          time.Time      `json:"paid_at"`                                                 // 支付时间
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"-"`
}

func (o *Order) TableName() string {
	return "orders"
}
