package model

import (
	"time"

	"gorm.io/gorm"
)

type User struct {
	ID           uint           `gorm:"primarykey" json:"id"`
	Username     string         `gorm:"uniqueIndex;size:50" json:"username"`
	Password     string         `gorm:"size:100" json:"-"`
	DisplayName  string         `gorm:"size:100" json:"display_name"`
	Email        string         `gorm:"size:100" json:"email"`
	Role         int            `gorm:"default:1" json:"role"` // 1=普通用户，100=管理员
	Status       int            `gorm:"default:1" json:"status"` // 1=启用，0=禁用
	Quota        int64          `gorm:"default:0" json:"quota"` // 用户配额，-1=无限
	UsedQuota    int64          `gorm:"default:0" json:"used_quota"` // 已用配额
	RequestCount int64          `gorm:"default:0" json:"request_count"` // 请求次数
	AccessToken  string         `gorm:"size:64" json:"access_token"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

func (u *User) TableName() string {
	return "users"
}
