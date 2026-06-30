package model

import (
	"time"

	"gorm.io/gorm"
)

type Token struct {
	ID             uint           `gorm:"primarykey" json:"id"`
	UserID         uint           `gorm:"index" json:"user_id"`
	Name           string         `gorm:"size:100" json:"name"`
	Key            string         `gorm:"uniqueIndex;size:100" json:"key"`
	Status         int            `gorm:"default:1" json:"status"` // 1=启用，2=禁用，3=过期
	RemainQuota    int64          `gorm:"default:0" json:"remain_quota"` // 剩余配额，-1=无限
	InitQuota      int64          `gorm:"default:0" json:"init_quota"` // 初始配额
	UnlimitedQuota bool           `gorm:"default:false" json:"unlimited_quota"` // 是否无限配额
	RateLimitGroup string         `gorm:"size:50;default:''" json:"rate_limit_group"` // 限流组：basic/standard/premium/pro/空=不限
	ActivatedAt    int64          `gorm:"default:0" json:"activated_at"` // 激活时间（首次使用时间），0=未激活
	ExpiredTime    int64          `gorm:"default:0" json:"expired_time"` // 过期时间，-1=永不过期
	CreatedTime    int64          `json:"created_time"`
	AccessedTime   int64          `json:"accessed_time"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

func (t *Token) TableName() string {
	return "tokens"
}
