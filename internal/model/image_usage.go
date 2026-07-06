package model

import "time"

// ImageUsage 图片使用记录（用于统计每日图片次数）
type ImageUsage struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	TokenID   uint      `gorm:"index:idx_image_token_time" json:"token_id"`  // 关联 token
	CreatedAt time.Time `json:"created_at"`
}

func (i *ImageUsage) TableName() string {
	return "image_usages"
}
