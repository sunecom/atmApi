package service

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"atmapi/internal/model"
)

// GenerateAPIKey 生成 sk-atm- 前缀的随机 API Key
// 格式: sk-atm-<32位随机hex>
func GenerateAPIKey() string {
	bytes := make([]byte, 16) // 16 bytes = 32 hex chars
	if _, err := rand.Read(bytes); err != nil {
		// fallback to timestamp-based (should never happen)
		return fmt.Sprintf("sk-atm-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("sk-atm-%s", hex.EncodeToString(bytes))
}

// CreateKeyWithPlan 创建 API Key 并绑定套餐
func CreateKeyWithPlan(userID uint, name string, planName string) (*model.Token, error) {
	// 验证套餐是否存在
	var plan model.Plan
	if err := model.DB.Where("name = ?", planName).First(&plan).Error; err != nil {
		return nil, fmt.Errorf("套餐 %s 不存在", planName)
	}

	// 生成 sk-atm- 前缀的 Key
	key := GenerateAPIKey()

	// 检查唯一性（极小概率冲突，但保险起见）
	var count int64
	model.DB.Model(&model.Token{}).Where("key = ?", key).Count(&count)
	for count > 0 {
		key = GenerateAPIKey()
		model.DB.Model(&model.Token{}).Where("key = ?", key).Count(&count)
	}

	token := model.Token{
		UserID:         userID,
		Name:           name,
		Key:            key,
		Status:         1,
		RemainQuota:    -1, // 无限配额（靠滑动窗口限流）
		InitQuota:      -1,
		UnlimitedQuota: true,
		RateLimitGroup: planName,
		CreatedTime:    time.Now().Unix(),
	}

	if err := model.DB.Create(&token).Error; err != nil {
		return nil, fmt.Errorf("创建 Token 失败: %w", err)
	}

	log.Printf("[Key生成] 用户=%d 名称=%s 套餐=%s key=%s...",
		userID, name, planName, key[:12])

	return &token, nil
}

// BindKeyToPlan 将已有 Key 绑定/切换套餐
func BindKeyToPlan(tokenID uint, planName string) error {
	// 验证套餐
	var plan model.Plan
	if err := model.DB.Where("name = ?", planName).First(&plan).Error; err != nil {
		return fmt.Errorf("套餐 %s 不存在", planName)
	}

	// 更新 token
	result := model.DB.Model(&model.Token{}).Where("id = ?", tokenID).Updates(map[string]interface{}{
		"rate_limit_group": planName,
		"unlimited_quota":  true,
		"remain_quota":     -1,
	})

	if result.Error != nil {
		return fmt.Errorf("绑定套餐失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("token %d 不存在", tokenID)
	}

	log.Printf("[Key绑定] tokenID=%d → 套餐=%s", tokenID, planName)
	return nil
}

// GetKeyPlan 查询 Key 当前绑定的套餐详情
func GetKeyPlan(tokenKey string) (*model.Token, *model.Plan, error) {
	var token model.Token
	if err := model.DB.Where("key = ?", tokenKey).First(&token).Error; err != nil {
		return nil, nil, fmt.Errorf("token 不存在")
	}

	if token.RateLimitGroup == "" {
		return &token, nil, nil // 无限流组
	}

	var plan model.Plan
	if err := model.DB.Where("name = ?", token.RateLimitGroup).First(&plan).Error; err != nil {
		return &token, nil, fmt.Errorf("套餐 %s 不存在", token.RateLimitGroup)
	}

	return &token, &plan, nil
}
