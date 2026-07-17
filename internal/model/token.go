package model

import (
	"log"
	"strings"
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
	RateLimitGroup string         `gorm:"size:50;default:''" json:"rate_limit_group"` // 限流组
	PlanGroup      string         `gorm:"size:50;default:''" json:"plan_group"`       // 套餐线：dp-a4/glm-5.2
	PlanName       string         `gorm:"size:50;default:''" json:"plan_name"`        // 套餐名：basic/pro/openclaw-pro
	ActivatedAt    int64          `gorm:"default:0" json:"activated_at"` // 激活时间
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

// FindByKey 安全的 token 查询（Raw SQL 方式）
// 注意：GORM 的 Where("key = ?", val) 会把值用双引号包裹，
// SQLite 将双引号值视为标识符而非字符串，导致查询失败。
// 同时 status 列在旧数据中可能存为文本（"active"/"disabled"），
// 需要用 CAST 转为 INTEGER 才能正常 Scan 到 Go 的 int 字段。
func FindByKey(key string) (*Token, error) {
	var token Token
	escaped := strings.ReplaceAll(key, "'", "''")
	sql := "SELECT id,user_id,name,`key`,CAST(status AS SIGNED) as status,remain_quota,init_quota,unlimited_quota,rate_limit_group,plan_group,plan_name,activated_at,expired_time,created_time,accessed_time,created_at,updated_at,deleted_at FROM tokens WHERE `key` = '" + escaped + "' AND deleted_at IS NULL LIMIT 1"
	log.Printf("[FindByKey] query for token name lookup")
	err := DB.Raw(sql).Scan(&token).Error
	if err != nil {
		log.Printf("[FindByKey] Error: %v", err)
		return nil, err
	}
	if token.ID == 0 {
		log.Printf("[FindByKey] Token not found (ID=0)")
		return nil, gorm.ErrRecordNotFound
	}
	log.Printf("[FindByKey] Found token: id=%d, name=%s", token.ID, token.Name)
	return &token, nil
}

// CountByKey 统计 key 出现次数
func CountByKey(key string) (int64, error) {
	var count int64
	escaped := strings.ReplaceAll(key, "'", "''")
	sql := "SELECT COUNT(*) FROM tokens WHERE \"key\" = '" + escaped + "'"
	err := DB.Raw(sql).Scan(&count).Error
	return count, err
}
