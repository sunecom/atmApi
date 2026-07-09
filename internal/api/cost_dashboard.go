package api

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"atmapi/internal/model"
	"atmapi/internal/service"

	"github.com/gin-gonic/gin"
)

// TokenRankingItem Token 排行项
type TokenRankingItem struct {
	TokenID       uint    `json:"token_id"`
	TokenName     string  `json:"token_name"`
	PlanName      string  `json:"plan_name"`
	TotalCalls    int64   `json:"total_calls"`
	TotalTokens   int64   `json:"total_tokens"`
	TotalCost     float64 `json:"total_cost"`
	Revenue       float64 `json:"revenue"`
	Profit        float64 `json:"profit"`
	ProfitMargin  float64 `json:"profit_margin"`
	IsLoss        bool    `json:"is_loss"`
}

// getTokenRanking 获取 Token 盈亏排行榜
func getTokenRanking(c *gin.Context) {
	period := c.DefaultQuery("period", "today")
	limitStr := c.DefaultQuery("limit", "20")
	
	var limit int
	if _, err := fmt.Sscanf(limitStr, "%d", &limit); err != nil || limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	// 计算时间范围
	now := time.Now()
	var startTime, endTime time.Time
	switch period {
	case "today":
		startTime = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		endTime = now
	case "7d":
		startTime = now.AddDate(0, 0, -7)
		endTime = now
	case "30d":
		startTime = now.AddDate(0, 0, -30)
		endTime = now
	case "month":
		startTime = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		endTime = now
	default:
		startTime = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		endTime = now
	}

	// 查询所有有使用记录的 token
	var usageLogs []model.UsageLog
	model.DB.Where("created_at BETWEEN ? AND ?", startTime, endTime).
		Find(&usageLogs)

	// 按 token 分组统计
	tokenStats := make(map[uint]*TokenRankingItem)
	for _, log := range usageLogs {
		if _, ok := tokenStats[log.TokenID]; !ok {
			tokenStats[log.TokenID] = &TokenRankingItem{
				TokenID:   log.TokenID,
				TokenName: log.TokenName,
				PlanName:  log.PlanName,
			}
		}
		item := tokenStats[log.TokenID]
		item.TotalCalls++
		item.TotalTokens += log.TotalTokens
		item.TotalCost += log.EstimatedCost
	}

	// 计算收入和利润
	var items []TokenRankingItem
	for _, item := range tokenStats {
		// 获取套餐信息计算收入
		var plan model.Plan
		if item.PlanName != "" {
			model.DB.Where("name = ?", item.PlanName).First(&plan)
		}

		// 收入 = 套餐价格 * (调用次数 / 套餐月次数)
		// 简化：假设套餐是月付，按调用次数占比计算
		if plan.Price != "" {
			var price float64
			fmt.Sscanf(plan.Price, "%f", &price)
			// 月次数 = MonthlyMax，如果为 0 则不限制
			if plan.MonthlyMax > 0 {
				item.Revenue = price * float64(item.TotalCalls) / float64(plan.MonthlyMax)
			} else {
				// 不限制套餐，按平均分配
				item.Revenue = price / 30.0 // 假设每天用 1/30
			}
		}

		item.Profit = item.Revenue - item.TotalCost
		if item.Revenue > 0 {
			item.ProfitMargin = (item.Profit / item.Revenue) * 100
		}
		item.IsLoss = item.Profit < 0

		items = append(items, *item)
	}

	// 排序：按利润降序
	sort.Slice(items, func(i, j int) bool {
		return items[i].Profit > items[j].Profit
	})

	// 分离赚钱和赔钱
	var profitable, loss []TokenRankingItem
	for _, item := range items {
		if item.IsLoss {
			loss = append(loss, item)
		} else {
			profitable = append(profitable, item)
		}
	}

	// 赔钱的按亏损金额排序（亏损最多的在前）
	sort.Slice(loss, func(i, j int) bool {
		return loss[i].Profit < loss[j].Profit
	})

	// 返回 Top N
	if len(profitable) > limit {
		profitable = profitable[:limit]
	}
	if len(loss) > limit {
		loss = loss[:limit]
	}

	c.JSON(http.StatusOK, gin.H{
		"period":     period,
		"profitable": profitable, // 赚钱 Top N
		"loss":       loss,       // 赔钱 Top N
		"total":      len(items),
	})
}

// AlertItem 预警项
type AlertItem struct {
	TokenID      uint    `json:"token_id"`
	TokenName    string  `json:"token_name"`
	PlanName     string  `json:"plan_name"`
	CurrentLoss  float64 `json:"current_loss"`
	CostTrend    string  `json:"cost_trend"` // "rising" / "stable" / "falling"
	RiskLevel    string  `json:"risk_level"` // "high" / "medium" / "low"
	Suggestion   string  `json:"suggestion"`
}

// getAlerts 获取预警列表
func getAlerts(c *gin.Context) {
	// 查询本月所有亏损 token
	now := time.Now()
	startTime := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	endTime := now

	var usageLogs []model.UsageLog
	model.DB.Where("created_at BETWEEN ? AND ?", startTime, endTime).
		Find(&usageLogs)

	// 按 token 分组
	tokenStats := make(map[uint]*struct {
		Name     string
		PlanName string
		Cost     float64
		Calls    int64
	})

	for _, log := range usageLogs {
		if _, ok := tokenStats[log.TokenID]; !ok {
			tokenStats[log.TokenID] = &struct {
				Name     string
				PlanName string
				Cost     float64
				Calls    int64
			}{
				Name:     log.TokenName,
				PlanName: log.PlanName,
			}
		}
		stats := tokenStats[log.TokenID]
		stats.Cost += log.EstimatedCost
		stats.Calls++
	}

	// 计算亏损并生成预警
	var alerts []AlertItem
	for tokenID, stats := range tokenStats {
		// 获取套餐信息
		var plan model.Plan
		if stats.PlanName != "" {
			model.DB.Where("name = ?", stats.PlanName).First(&plan)
		}

		// 计算收入
		var revenue float64
		if plan.Price != "" {
			var price float64
			fmt.Sscanf(plan.Price, "%f", &price)
			if plan.MonthlyMax > 0 {
				revenue = price * float64(stats.Calls) / float64(plan.MonthlyMax)
			}
		}

		profit := revenue - stats.Cost
		if profit < 0 {
			// 亏损，生成预警
			riskLevel := "low"
			if stats.Cost > revenue*2 {
				riskLevel = "high"
			} else if stats.Cost > revenue*1.5 {
				riskLevel = "medium"
			}

			suggestion := "建议关注使用情况"
			if riskLevel == "high" {
				suggestion = "建议限制调用频率或调整套餐"
			} else if riskLevel == "medium" {
				suggestion = "建议监控使用趋势"
			}

			alerts = append(alerts, AlertItem{
				TokenID:     tokenID,
				TokenName:   stats.Name,
				PlanName:    stats.PlanName,
				CurrentLoss: -profit,
				CostTrend:   "stable", // TODO: 实现趋势检测
				RiskLevel:   riskLevel,
				Suggestion:  suggestion,
			})
		}
	}

	// 按亏损金额排序
	sort.Slice(alerts, func(i, j int) bool {
		return alerts[i].CurrentLoss > alerts[j].CurrentLoss
	})

	c.JSON(http.StatusOK, gin.H{
		"period":  "month",
		"alerts":  alerts,
		"count":   len(alerts),
	})
}

// getDashboardEnhanced 增强版仪表盘（含趋势数据）
func getDashboardEnhanced(c *gin.Context) {
	period := c.DefaultQuery("period", "today")

	now := time.Now()
	var startTime, endTime time.Time
	switch period {
	case "today":
		startTime = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		endTime = now
	case "7d":
		startTime = now.AddDate(0, 0, -7)
		endTime = now
	case "30d":
		startTime = now.AddDate(0, 0, -30)
		endTime = now
	case "month":
		startTime = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		endTime = now
	default:
		startTime = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		endTime = now
	}

	// 获取基础汇总
	summary, err := service.GetDashboardSummary(startTime, endTime)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 获取趋势数据（按天分组）
	type DailyTrend struct {
		Date    string  `json:"date"`
		Revenue float64 `json:"revenue"`
		Cost    float64 `json:"cost"`
		Profit  float64 `json:"profit"`
	}

	// TODO: 实现按天分组查询
	trend := []DailyTrend{}

	c.JSON(http.StatusOK, gin.H{
		"data":   summary,
		"trend":  trend,
		"period": period,
	})
}
