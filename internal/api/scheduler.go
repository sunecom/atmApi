package api

import (
	"fmt"
	"log"
	"os"
	"time"

	"atmapi/internal/model"
	"atmapi/internal/service"
)

// StartCostAlerter 启动成本告警定时任务
// 每小时检查一次，当 token 成本超过收入时发送飞书告警
func StartCostAlerter() {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		// 启动时立即执行一次
		checkCostAlerts()

		for range ticker.C {
			checkCostAlerts()
		}
	}()
	log.Println("[成本告警] 定时任务已启动（每小时检查一次）")
}

// checkCostAlerts 检查成本告警
func checkCostAlerts() {
	// 获取当前月份的时间范围
	now := time.Now()
	startTime := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	endTime := now

	// 查询所有有套餐的活跃 token
	var tokens []model.Token
	model.DB.Where("status = 1 AND rate_limit_group != ''").Find(&tokens)

	for _, token := range tokens {
		// 获取该 token 的成本汇总
		summary, err := service.GetTokenCostSummary(token.ID, startTime, endTime)
		if err != nil {
			log.Printf("[成本告警] Token %s 查询失败: %v", token.Name, err)
			continue
		}

		// 检查是否亏损（成本 > 收入）
		isLoss, profit, _ := service.CheckTokenLoss(token.ID)
		if isLoss {
			log.Printf("[成本告警] Token %s 本月亏损: %.2f 元 (成本: %.2f, 收入: %.2f)",
				token.Name, -profit, summary.TotalCost, summary.Revenue)

			// 发送飞书告警
			sendCostAlert(token, summary, profit)
		}
	}
}

// sendCostAlert 发送成本告警到飞书
func sendCostAlert(token model.Token, summary *service.TokenCostSummary, profit float64) {
	// 检查今天是否已经告警过（避免重复告警）
	alertKey := fmt.Sprintf("cost_alert_%d_%s", token.ID, time.Now().Format("2006-01-02"))
	var count int64
	model.DB.Model(&model.RequestLog{}).
		Where("token_name = ? AND channel_name = ? AND created_at >= ?",
			token.Name, alertKey, time.Now().Format("2006-01-02")).
		Count(&count)

	if count > 0 {
		return // 今天已告警过
	}

	// 记录告警日志（用 RequestLog 标记，避免重复）
	model.DB.Create(&model.RequestLog{
		TokenName:   token.Name,
		ChannelName: alertKey,
		Model:       "cost_alert",
		StatusCode:  0,
		DurationMs:  0,
	})

	// 构建告警消息
	msg := fmt.Sprintf("⚠️ **成本告警**\n\n"+
		"Token: %s\n"+
		"套餐: %s\n"+
		"本月成本: ¥%.2f\n"+
		"本月收入: ¥%.2f\n"+
		"亏损: ¥%.2f\n"+
		"利润率: %.1f%%\n\n"+
		"建议：检查该 token 的使用情况，考虑调整套餐或限制用量。",
		token.Name, summary.PlanName, summary.TotalCost, summary.Revenue, -profit, summary.ProfitMargin)

	// 写入告警文件（由外部脚本读取并发送到飞书）
	alertFile := "/tmp/atmapi-cost-alert"
	f, err := os.OpenFile(alertFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[成本告警] 写入告警文件失败: %v", err)
		return
	}
	defer f.Close()

	f.WriteString(fmt.Sprintf("[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), msg))
	log.Printf("[成本告警] 已写入告警文件: %s", alertFile)
}

// StartExpiryChecker 启动过期 Token 自动禁用定时任务
// 每 10 分钟扫描一次，将过期的 token 状态设为 3（过期）
func StartExpiryChecker() {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()

		// 启动时立即执行一次
		checkExpiredTokens()

		for range ticker.C {
			checkExpiredTokens()
		}
	}()
	log.Println("[过期检查] 定时任务已启动（每10分钟扫描一次）")
}

// checkExpiredTokens 扫描并禁用过期 token
func checkExpiredTokens() {
	now := time.Now().Unix()

	// 查找已过期但状态还是启用的 token
	var expired []model.Token
	model.DB.Where("status = 1 AND expired_time > 0 AND expired_time < ?", now).Find(&expired)

	if len(expired) == 0 {
		return
	}

	disabledCount := 0
	for _, token := range expired {
		// 将状态设为 3（过期）
		model.DB.Model(&token).Update("status", 3)
		disabledCount++

		// 到期前7天提醒（如果 token 在7天内到期且还在用，记录日志）
		log.Printf("[过期检查] Token %s (id=%d) 已过期，自动禁用。到期时间: %s",
			token.Name, token.ID,
			time.Unix(token.ExpiredTime, 0).Format("2006-01-02 15:04:05"))
	}

	if disabledCount > 0 {
		log.Printf("[过期检查] 本次禁用 %d 个过期 token", disabledCount)
	}

	// 同时清理超过31天的限流记录
	var oldCount int64
	model.DB.Model(&model.RateLimit{}).
		Where("request_time < ?", now-31*24*3600).
		Count(&oldCount)
	if oldCount > 0 {
		model.DB.Where("request_time < ?", now-31*24*3600).Delete(&model.RateLimit{})
		log.Printf("[过期检查] 清理 %d 条过期限流记录", oldCount)
	}
}

// StartUsageAlerter 启动用量告警定时任务
// 每小时检查一次，当 token 用量达到 80%/90%/100% 时记录告警日志
func StartUsageAlerter() {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for range ticker.C {
			checkUsageAlerts()
		}
	}()
	log.Println("[用量告警] 定时任务已启动（每小时检查一次）")
}

// checkUsageAlerts 检查用量告警
func checkUsageAlerts() {
	var tokens []model.Token
	// 只检查有套餐的活跃 token
	model.DB.Where("status = 1 AND rate_limit_group != ''").Find(&tokens)

	for _, token := range tokens {
		rlResult := CheckRateLimitForAlerts(&token)
		if rlResult == nil {
			continue
		}

		// 5小时配额告警
		if rlResult.Limit5h > 0 {
			pct := rlResult.Used5h * 100 / rlResult.Limit5h
			if pct >= 100 {
				log.Printf("[用量告警] Token %s 5小时配额已用完 (%d/%d)",
					token.Name, rlResult.Used5h, rlResult.Limit5h)
			} else if pct >= 90 {
				log.Printf("[用量告警] Token %s 5小时配额使用 90%%+ (%d/%d)",
					token.Name, rlResult.Used5h, rlResult.Limit5h)
			}
		}

		// 月配额告警
		if rlResult.LimitMonthly > 0 {
			pct := rlResult.UsedMonthly * 100 / rlResult.LimitMonthly
			if pct >= 80 {
				log.Printf("[用量告警] Token %s 月配额使用 %d%% (%d/%d)",
					token.Name, pct, rlResult.UsedMonthly, rlResult.LimitMonthly)
			}
		}
	}
}

// CheckRateLimitForAlerts 用于告警检查的限流查询（不阻断）
func CheckRateLimitForAlerts(token *model.Token) *RateLimitAlertData {
	if token.RateLimitGroup == "" {
		return nil
	}

	plan, err := service.GetPlan(token.RateLimitGroup)
	if err != nil {
		return nil
	}

	now := time.Now().Unix()
	result := &RateLimitAlertData{
		Limit5h:      plan.Hourly5Max,
		LimitDaily:   plan.DailyMax,
		LimitWeekly:  plan.WeeklyMax,
		LimitMonthly: plan.MonthlyMax,
	}

	if plan.Hourly5Max > 0 {
		window5h := now - 5*3600
		model.DB.Model(&model.RateLimit{}).
			Where("token_id = ? AND request_time > ?", token.ID, window5h).
			Count(&result.Used5h)
	}

	if plan.DailyMax > 0 {
		dayAgo := now - 24*3600
		model.DB.Model(&model.RateLimit{}).
			Where("token_id = ? AND request_time > ?", token.ID, dayAgo).
			Count(&result.UsedDaily)
	}

	if plan.MonthlyMax > 0 {
		monthAgo := now - 30*24*3600
		model.DB.Model(&model.RateLimit{}).
			Where("token_id = ? AND request_time > ?", token.ID, monthAgo).
			Count(&result.UsedMonthly)
	}

	return result
}

// RateLimitAlertData 告警用的限流数据
type RateLimitAlertData struct {
	Used5h       int64
	Limit5h      int64
	UsedDaily    int64
	LimitDaily   int64
	UsedWeekly   int64
	LimitWeekly  int64
	UsedMonthly  int64
	LimitMonthly int64
}
