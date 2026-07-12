package api

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"atmapi/internal/model"

	"github.com/gin-gonic/gin"
)

// DashboardV2Response v2 仪表盘响应
type DashboardV2Response struct {
	AtmModel       string                `json:"atm_model"`
	Period         string                `json:"period"`
	CoreMetrics    CoreMetrics           `json:"core_metrics"`
	UpstreamDist   []UpstreamDistItem    `json:"upstream_distribution"`
	TokenRanking   TokenRankingResult    `json:"token_ranking"`
	DailyTrend     []DailyTrendItem      `json:"daily_trend"`
	Alerts         []AlertItemV2         `json:"alerts"`
}

type CoreMetrics struct {
	TotalRevenue  float64 `json:"total_revenue"`
	TotalCost     float64 `json:"total_cost"`
	TotalProfit   float64 `json:"total_profit"`
	ProfitMargin  float64 `json:"profit_margin"`
	TotalRequests int64   `json:"total_requests"`
	TotalTokens   int64   `json:"total_tokens"`
}

type UpstreamDistItem struct {
	Model      string  `json:"model"`
	Requests   int64   `json:"requests"`
	Tokens     int64   `json:"tokens"`
	Cost       float64 `json:"cost"`
	Percentage float64 `json:"percentage"`
}

type TokenRankingResult struct {
	Profitable []TokenRankItem `json:"profitable"`
	Loss       []TokenRankItem `json:"loss"`
}

type TokenRankItem struct {
	TokenName    string  `json:"token_name"`
	PlanName     string  `json:"plan_name"`
	TotalCalls   int64   `json:"total_calls"`
	TotalTokens  int64   `json:"total_tokens"`
	TotalCost    float64 `json:"total_cost"`
	Revenue      float64 `json:"revenue"`
	Profit       float64 `json:"profit"`
	ProfitMargin float64 `json:"profit_margin"`
}

type DailyTrendItem struct {
	Date    string  `json:"date"`
	Revenue float64 `json:"revenue"`
	Cost    float64 `json:"cost"`
	Profit  float64 `json:"profit"`
}

type AlertItemV2 struct {
	TokenName   string  `json:"token_name"`
	PlanName    string  `json:"plan_name"`
	CurrentLoss float64 `json:"current_loss"`
	RiskLevel   string  `json:"risk_level"`
	Suggestion  string  `json:"suggestion"`
}

// getDashboardV2 v2 仪表盘 API - 按 atm模型 维度
func getDashboardV2(c *gin.Context) {
	atmModel := c.DefaultQuery("atm_model", "")
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
	case "quarter":
		quarter := (int(now.Month()) - 1) / 3
		qStart := time.Month(quarter*3 + 1)
		startTime = time.Date(now.Year(), qStart, 1, 0, 0, 0, 0, now.Location())
		endTime = now
	case "year":
		startTime = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
		endTime = now
	default:
		startTime = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		endTime = now
	}

	// 获取所有 atm_model 列表（用于轮播）
	var atmModels []string
	model.DB.Raw(`SELECT DISTINCT COALESCE(NULLIF(atm_model,''), '未分类') as atm_model FROM channels WHERE deleted_at IS NULL ORDER BY atm_model`).Scan(&atmModels)

	// 构建 channel_name -> atm_model 映射
	type chMapping struct {
		Name     string
		AtmModel string
	}
	var channelMappings []chMapping
	model.DB.Raw(`SELECT name, COALESCE(NULLIF(atm_model,''), '未分类') as atm_model FROM channels WHERE deleted_at IS NULL`).Scan(&channelMappings)
	chToAtm := make(map[string]string)
	for _, m := range channelMappings {
		chToAtm[m.Name] = m.AtmModel
	}

	// 查询 usage_logs
	query := model.DB.Where("created_at BETWEEN ? AND ?", startTime, endTime)
	var usageLogs []model.UsageLog
	query.Find(&usageLogs)

	// 按 atm_model 过滤
	filteredLogs := make([]model.UsageLog, 0)
	for _, log := range usageLogs {
		logAtm := chToAtm[log.ChannelName]
		if logAtm == "" {
			logAtm = "未分类"
		}
		if atmModel == "" || logAtm == atmModel {
			filteredLogs = append(filteredLogs, log)
		}
	}

	// 1. 核心指标
	var totalCost float64
	var totalTokens int64
	for _, log := range filteredLogs {
		// 实时计算成本
		cost := model.CalculateCost(log.InputTokens, log.OutputTokens, log.CachedTokens, log.Model)
		totalCost += cost
		totalTokens += log.TotalTokens
	}

	// 计算收入（按套餐分摊）
	totalRevenue := calculateRevenue(filteredLogs)
	totalProfit := totalRevenue - totalCost
	profitMargin := 0.0
	if totalRevenue > 0 {
		profitMargin = (totalProfit / totalRevenue) * 100
	}

	coreMetrics := CoreMetrics{
		TotalRevenue:  totalRevenue,
		TotalCost:     totalCost,
		TotalProfit:   totalProfit,
		ProfitMargin:  profitMargin,
		TotalRequests: int64(len(filteredLogs)),
		TotalTokens:   totalTokens,
	}

	// 2. 上游大模型成本分布（按实际调用模型分类）
	upstreamMap := make(map[string]*UpstreamDistItem)
	for _, log := range filteredLogs {
		m := log.Model
		if m == "" {
			m = "未知"
		}
		if _, ok := upstreamMap[m]; !ok {
			upstreamMap[m] = &UpstreamDistItem{Model: m}
		}
		upstreamMap[m].Requests++
		upstreamMap[m].Tokens += log.TotalTokens
		upstreamMap[m].Cost += model.CalculateCost(log.InputTokens, log.OutputTokens, log.CachedTokens, log.Model)
	}
	var upstreamDist []UpstreamDistItem
	for _, item := range upstreamMap {
		if totalCost > 0 {
			item.Percentage = (item.Cost / totalCost) * 100
		}
		upstreamDist = append(upstreamDist, *item)
	}
	sort.Slice(upstreamDist, func(i, j int) bool {
		return upstreamDist[i].Cost > upstreamDist[j].Cost
	})

	// 3. Token 盈亏排行
	tokenStats := make(map[uint]*TokenRankItem)
	for _, log := range filteredLogs {
		if _, ok := tokenStats[log.TokenID]; !ok {
			tokenStats[log.TokenID] = &TokenRankItem{
				TokenName: log.TokenName,
				PlanName:  log.PlanName,
			}
		}
		item := tokenStats[log.TokenID]
		item.TotalCalls++
		item.TotalTokens += log.TotalTokens
		item.TotalCost += model.CalculateCost(log.InputTokens, log.OutputTokens, log.CachedTokens, log.Model)
	}
	// 计算每个 token 的收入
	for _, item := range tokenStats {
		item.Revenue = calculateTokenRevenue(item.PlanName)
		item.Profit = item.Revenue - item.TotalCost
		if item.Revenue > 0 {
			item.ProfitMargin = (item.Profit / item.Revenue) * 100
		}
	}
	var profitable, loss []TokenRankItem
	for _, item := range tokenStats {
		if item.Profit >= 0 {
			profitable = append(profitable, *item)
		} else {
			loss = append(loss, *item)
		}
	}
	sort.Slice(profitable, func(i, j int) bool { return profitable[i].Profit > profitable[j].Profit })
	sort.Slice(loss, func(i, j int) bool { return loss[i].Profit < loss[j].Profit })
	// Top 10
	if len(profitable) > 10 {
		profitable = profitable[:10]
	}
	if len(loss) > 10 {
		loss = loss[:10]
	}

	// 4. 每日趋势
	// 收入按天分摊：套餐月费 / 30 天
	dayRevenue := totalRevenue / 30.0 // 套餐月费按30天日均
	dailyMap := make(map[string]*DailyTrendItem)
	for _, log := range filteredLogs {
		date := log.CreatedAt.Format("2006-01-02")
		if _, ok := dailyMap[date]; !ok {
			dailyMap[date] = &DailyTrendItem{Date: date}
		}
		dailyMap[date].Cost += model.CalculateCost(log.InputTokens, log.OutputTokens, log.CachedTokens, log.Model)
	}
	for _, item := range dailyMap {
		item.Revenue = dayRevenue
		item.Profit = item.Revenue - item.Cost
	}
	var dailyTrend []DailyTrendItem
	for _, item := range dailyMap {
		dailyTrend = append(dailyTrend, *item)
	}
	sort.Slice(dailyTrend, func(i, j int) bool { return dailyTrend[i].Date < dailyTrend[j].Date })

	// 5. 预警
	var alerts []AlertItemV2
	for _, item := range loss {
		riskLevel := "low"
		if item.TotalCost > item.Revenue*2 {
			riskLevel = "high"
		} else if item.TotalCost > item.Revenue*1.5 {
			riskLevel = "medium"
		}
		suggestion := "建议关注使用情况"
		if riskLevel == "high" {
			suggestion = "建议限制调用频率或调整套餐"
		} else if riskLevel == "medium" {
			suggestion = "建议监控使用趋势"
		}
		alerts = append(alerts, AlertItemV2{
			TokenName:   item.TokenName,
			PlanName:    item.PlanName,
			CurrentLoss: -item.Profit,
			RiskLevel:   riskLevel,
			Suggestion:  suggestion,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"atm_models":              atmModels,
		"current_atm_model":       atmModel,
		"period":                  period,
		"core_metrics":            coreMetrics,
		"upstream_distribution":   upstreamDist,
		"token_ranking":           TokenRankingResult{Profitable: profitable, Loss: loss},
		"daily_trend":             dailyTrend,
		"alerts":                  alerts,
	})
}

// calculateRevenue 计算一组 usage_logs 的收入
func calculateRevenue(logs []model.UsageLog) float64 {
	// 按 token 分组统计调用次数
	tokenCalls := make(map[uint]struct {
		PlanName string
		Calls    int64
	})
	for _, log := range logs {
		tc := tokenCalls[log.TokenID]
		tc.PlanName = log.PlanName
		tc.Calls++
		tokenCalls[log.TokenID] = tc
	}

	var total float64
	for _, tc := range tokenCalls {
		total += calculateTokenRevenue(tc.PlanName)
	}
	return total
}

// calculateTokenRevenue 计算单个 token 的收入
// 用户购买套餐时一次性付月服务费（= 套餐售价），收入立即确认
func calculateTokenRevenue(planName string) float64 {
	if planName == "" {
		return 0
	}
	var plan model.Plan
	if err := model.DB.Where("name = ?", planName).First(&plan).Error; err != nil {
		return 0
	}
	var price float64
	fmt.Sscanf(plan.Price, "%f", &price)
	return price // 一次性确认收入
}
