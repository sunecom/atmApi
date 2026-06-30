package service

import (
	"fmt"
	"log"
	"time"

	"atmapi/internal/model"
)

// 套餐配额配置
// key: token 的 rate_limit_group 字段值
// value: {5小时峰值, 每周总量}
var rateLimitPlans = map[string]struct {
	Hourly5Max  int64
	WeeklyMax   int64
	SkipHourly  bool // true 表示跳过5小时限流
}{
	"basic":    {Hourly5Max: 500, WeeklyMax: 40000},
	"standard": {Hourly5Max: 1000, WeeklyMax: 40000},
	"premium":  {Hourly5Max: 1500, WeeklyMax: 40000},
	"pro":      {Hourly5Max: 2000, WeeklyMax: 40000},
	"weekly":   {WeeklyMax: 40000, SkipHourly: true}, // 仅周限，不限5小时
}

// GetPlan 获取套餐配置
func GetPlan(planName string) (*model.Plan, error) {
	var plan model.Plan
	err := model.DB.Where("name = ?", planName).First(&plan).Error
	if err != nil {
		return nil, err
	}
	return &plan, nil
}

// CheckRateLimit 滑动窗口限流检查
// 返回 (是否允许, 错误信息)
func CheckRateLimit(token *model.Token) (bool, string) {
	// 没有设置限流组的 token 不限滑动窗口
	// （unlimited_quota 只控制总量，不影响滑动窗口限流）
	if token.RateLimitGroup == "" {
		return true, ""
	}

	// 从数据库获取套餐配置
	plan, err := GetPlan(token.RateLimitGroup)
	if err != nil {
		log.Printf("[限流] token %s 的限流组 %s 未配置，跳过限流", token.Key, token.RateLimitGroup)
		return true, ""
	}

	now := time.Now().Unix()

	// 检查 5 小时窗口（如果 SkipHourly 为 true 则跳过）
	if !plan.SkipHourly && plan.Hourly5Max > 0 {
		window5hStart := now - 5*3600
		var count5h int64
		model.DB.Model(&model.RateLimit{}).
			Where("token_id = ? AND request_time > ?", token.ID, window5hStart).
			Count(&count5h)

		if count5h >= plan.Hourly5Max {
			return false, fmt.Sprintf("5小时配额已用完（%d/%d），请稍后再试", count5h, plan.Hourly5Max)
		}
	}

	// 检查每周窗口（7天 = 168小时）
	if plan.WeeklyMax > 0 {
		weekStart := now - 7*24*3600
		var countWeek int64
		model.DB.Model(&model.RateLimit{}).
			Where("token_id = ? AND request_time > ?", token.ID, weekStart).
			Count(&countWeek)

		if countWeek >= plan.WeeklyMax {
			return false, fmt.Sprintf("每周配额已用完（%d/%d），请下周再试", countWeek, plan.WeeklyMax)
		}
	}

	return true, ""
}

// RecordRequest 记录一次请求（用于滑动窗口计数）
func RecordRequest(tokenID uint) {
	rateLimit := model.RateLimit{
		TokenID:     tokenID,
		RequestTime: time.Now().Unix(),
	}
	model.DB.Create(&rateLimit)
}

// CleanOldRecords 清理过期记录（超过7天的）
// 建议每天调用一次
func CleanOldRecords() {
	sevenDaysAgo := time.Now().Add(-7 * 24 * time.Hour).Unix()
	model.DB.Where("request_time < ?", sevenDaysAgo).Delete(&model.RateLimit{})
	log.Printf("[限流] 清理7天前的限流记录完成")
}
