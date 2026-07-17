package service

import (
	"fmt"
	"log"
	"math"
	"time"

	"atmapi/internal/model"
)

// GLMPointsDeductor GLM-5.2 点数扣减器
type GLMPointsDeductor struct{}

// DeductResult 扣点结果
type DeductResult struct {
	Success       bool
	PointsDeducted int
	Reason        string
	RemainingPoints int
}

// DeductPoints 按标准结算价扣点
// 核心逻辑：按标准价扣点，不是按实际成本
func (d *GLMPointsDeductor) DeductPoints(tokenID uint, inputTokens, outputTokens int64, cacheHit bool, failed bool) *DeductResult {
	// 1. 失败请求不扣用户点
	if failed {
		log.Printf("[GLM点数] token=%d 失败请求，不扣点", tokenID)
		return &DeductResult{Success: true, PointsDeducted: 0, Reason: "failed_request"}
	}

	// 2. 缓存命中扣0点
	if cacheHit {
		log.Printf("[GLM点数] token=%d 缓存命中，扣0点", tokenID)
		return &DeductResult{Success: true, PointsDeducted: 0, Reason: "cache_hit"}
	}

	// 3. 按标准结算价计算扣点
	// 标准价：100点 = ¥1，即 1点 = ¥0.01
	// 标准成本 = (inputTokens * 0.000008 + outputTokens * 0.000028) * 100
	// 简化：每1000 input tokens = 8点，每1000 output tokens = 28点
	standardPoints := int(math.Ceil(float64(inputTokens)*0.008 + float64(outputTokens)*0.028))
	if standardPoints < 1 {
		standardPoints = 1 // 最少扣1点
	}

	// 4. 查询当前账本
	now := time.Now()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	var ledger model.GLMPointsLedger
	err := model.DB.Where("token_id = ? AND period_start = ?", tokenID, periodStart).First(&ledger).Error
	if err != nil {
		// 账本不存在，说明不是GLM套餐用户，跳过扣点
		log.Printf("[GLM点数] token=%d 无账本，跳过扣点", tokenID)
		return &DeductResult{Success: true, PointsDeducted: 0, Reason: "no_ledger"}
	}

	// 5. 检查余额
	if ledger.UsedPoints+standardPoints > ledger.TotalPoints {
		return &DeductResult{
			Success: false,
			PointsDeducted: 0,
			Reason: "insufficient_points",
			RemainingPoints: ledger.TotalPoints - ledger.UsedPoints,
		}
	}

	// 6. 原子扣减（检查 RowsAffected）
	result := model.DB.Model(&model.GLMPointsLedger{}).
		Where("id = ? AND used_points + ? <= total_points", ledger.ID, standardPoints).
		Update("used_points", model.DB.Raw("used_points + ?", standardPoints))
	if result.Error != nil || result.RowsAffected == 0 {
		log.Printf("[GLM点数] token=%d 扣点失败: err=%v rows=%d", tokenID, result.Error, result.RowsAffected)
		return &DeductResult{Success: false, PointsDeducted: 0, Reason: "deduct_failed"}
	}

	// 7. 更新5h和日护栏
	fiveHourWindowStart := now.Add(-5 * time.Hour)
	dailyWindowStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// 重置5h和日用量（简单实现：每次扣点时检查窗口）
	model.DB.Model(&model.GLMPointsLedger{}).
		Where("id = ?", ledger.ID).
		Updates(map[string]interface{}{
			"five_hour_used": model.DB.Raw("(SELECT COUNT(*) FROM rate_limits WHERE token_id = ? AND request_time > ?)", tokenID, fiveHourWindowStart),
			"daily_used":     model.DB.Raw("(SELECT COUNT(*) FROM rate_limits WHERE token_id = ? AND request_time > ?)", tokenID, dailyWindowStart),
		})

	remaining := ledger.TotalPoints - ledger.UsedPoints - standardPoints
	log.Printf("[GLM点数] token=%d 扣点成功: %d点, 剩余: %d点", tokenID, standardPoints, remaining)

	return &DeductResult{
		Success: true,
		PointsDeducted: standardPoints,
		RemainingPoints: remaining,
	}
}

// CheckBalance 检查余额是否足够
func (d *GLMPointsDeductor) CheckBalance(tokenID uint, estimatedPoints int) (bool, int) {
	now := time.Now()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	var ledger model.GLMPointsLedger
	err := model.DB.Where("token_id = ? AND period_start = ?", tokenID, periodStart).First(&ledger).Error
	if err != nil {
		return false, 0 // fail-closed：无账本不 ProvisionallyAllow
	}

	remaining := ledger.TotalPoints - ledger.UsedPoints
	return remaining >= estimatedPoints, remaining
}

// CreateLedger 创建账本（购买套餐时调用）
func (d *GLMPointsDeductor) CreateLedger(tokenID uint, planName string, totalPoints, fiveHourPoints, dailyPoints int) error {
	now := time.Now()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	periodEnd := periodStart.AddDate(0, 1, 0)

	ledger := model.GLMPointsLedger{
		TokenID: tokenID,
		PlanName: planName,
		PeriodStart: periodStart,
		PeriodEnd: periodEnd,
		TotalPoints: totalPoints,
		UsedPoints: 0,
		FiveHourPoints: fiveHourPoints,
		FiveHourUsed: 0,
		DailyPoints: dailyPoints,
		DailyUsed: 0,
		StandardPricePerPoint: 0.01,
	}

	return model.DB.Create(&ledger).Error
}

// GetUsage 获取使用情况
func (d *GLMPointsDeductor) GetUsage(tokenID uint) (used, total int, err error) {
	now := time.Now()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	var ledger model.GLMPointsLedger
	err = model.DB.Where("token_id = ? AND period_start = ?", tokenID, periodStart).First(&ledger).Error
	if err != nil {
		return 0, 0, err
	}

	return ledger.UsedPoints, ledger.TotalPoints, nil
}

// 全局实例
var GLMDeductor = &GLMPointsDeductor{}

// EstimateStandardPoints 估算标准扣点（用于预检查）
func EstimateStandardPoints(inputTokens, outputTokens int) int {
	points := int(math.Ceil(float64(inputTokens)*0.008 + float64(outputTokens)*0.028))
	if points < 1 {
		points = 1
	}
	return points
}

// InitGLMLedgerIfNeeded 为GLM套餐用户初始化账本
func InitGLMLedgerIfNeeded(tokenID uint, planName string) error {
	// 检查是否已有账本
	now := time.Now()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	var count int64
	model.DB.Model(&model.GLMPointsLedger{}).
		Where("token_id = ? AND period_start = ?", tokenID, periodStart).
		Count(&count)

	if count > 0 {
		return nil // 已有账本
	}

	// 根据套餐获取点数配置
	var plan model.Plan
	err := model.DB.Where("name = ?", planName).First(&plan).Error
	if err != nil {
		return fmt.Errorf("套餐未找到: %s", planName)
	}

	// 从plan的扩展字段获取GLM点数配置
	// 这里简化处理，实际应该从plan的JSON字段读取
	totalPoints := 4200  // 默认体验版
	fiveHourPoints := 420
	dailyPoints := 840

	switch planName {
	case "glm52-basic":
		totalPoints = 4200
		fiveHourPoints = 420
		dailyPoints = 840
	case "glm52-standard":
		totalPoints = 10800
		fiveHourPoints = 1080
		dailyPoints = 2160
	case "glm52-pro":
		totalPoints = 25000
		fiveHourPoints = 2500
		dailyPoints = 5000
	}

	return GLMDeductor.CreateLedger(tokenID, planName, totalPoints, fiveHourPoints, dailyPoints)
}
