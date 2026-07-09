package service

import (
	"atmapi/internal/model"
	"fmt"
	"time"
)

// TokenCostSummary Token 成本汇总
type TokenCostSummary struct {
	TokenID        uint      `json:"token_id"`
	TokenName      string    `json:"token_name"`
	PlanName       string    `json:"plan_name"`
	TotalRequests  int64     `json:"total_requests"`  // 总请求数
	TotalInput     int64     `json:"total_input"`     // 总输入 token
	TotalOutput    int64     `json:"total_output"`    // 总输出 token
	TotalCost      float64   `json:"total_cost"`      // 总成本（元）
	AvgCostPerReq  float64   `json:"avg_cost_per_req"` // 平均每次请求成本
	Revenue        float64   `json:"revenue"`         // 收入（元）
	Profit         float64   `json:"profit"`          // 利润（元）
	ProfitMargin   float64   `json:"profit_margin"`   // 利润率（%）
	Period         string    `json:"period"`          // 统计周期
}

// GetTokenCostSummary 获取单个 Token 的成本汇总
func GetTokenCostSummary(tokenID uint, startTime, endTime time.Time) (*TokenCostSummary, error) {
	var logs []model.UsageLog
	err := model.DB.Where("token_id = ? AND created_at BETWEEN ? AND ?", tokenID, startTime, endTime).
		Find(&logs).Error
	if err != nil {
		return nil, err
	}

	if len(logs) == 0 {
		return &TokenCostSummary{TokenID: tokenID, Period: "custom"}, nil
	}

	summary := &TokenCostSummary{
		TokenID:   tokenID,
		Period:    startTime.Format("2006-01-02") + " ~ " + endTime.Format("2006-01-02"),
	}

	for _, log := range logs {
		summary.TotalRequests++
		summary.TotalInput += log.InputTokens
		summary.TotalOutput += log.OutputTokens
		summary.TotalCost += model.CalculateCost(log.InputTokens, log.OutputTokens, log.Model)
	}

	summary.AvgCostPerReq = summary.TotalCost / float64(summary.TotalRequests)

	// 获取 Token 信息计算收入
	var token model.Token
	model.DB.First(&token, tokenID)
	summary.TokenName = token.Name
	summary.PlanName = token.RateLimitGroup

	// 获取套餐价格
	var plan model.Plan
	model.DB.Where("name = ?", token.RateLimitGroup).First(&plan)
	if plan.Price != "" {
		// 解析价格（假设格式为 "29.9"）
		var price float64
		_, err := fmt.Sscanf(plan.Price, "%f", &price)
		if err == nil {
			summary.Revenue = price
			summary.Profit = summary.Revenue - summary.TotalCost
			if summary.Revenue > 0 {
				summary.ProfitMargin = (summary.Profit / summary.Revenue) * 100
			}
		}
	}

	return summary, nil
}

// DashboardSummary 仪表盘汇总
type DashboardSummary struct {
	TotalTokens      int64              `json:"total_tokens"`       // 活跃 token 数
	TotalRequests    int64              `json:"total_requests"`     // 总请求数
	TotalCost        float64            `json:"total_cost"`         // 总成本
	TotalRevenue     float64            `json:"total_revenue"`      // 总收入
	TotalProfit      float64            `json:"total_profit"`       // 总利润
	ProfitMargin     float64            `json:"profit_margin"`      // 利润率
	ByPlan           []PlanCostSummary  `json:"by_plan"`            // 按套餐分组
	ByModel          []ModelCostSummary `json:"by_model"`           // 按模型分组
	Period           string             `json:"period"`
}

// PlanCostSummary 按套餐分组的成本汇总
type PlanCostSummary struct {
	PlanName      string  `json:"plan_name"`
	TokenCount    int64   `json:"token_count"`
	Requests      int64   `json:"requests"`
	Cost          float64 `json:"cost"`
	Revenue       float64 `json:"revenue"`
	Profit        float64 `json:"profit"`
	ProfitMargin  float64 `json:"profit_margin"`
}

// ModelCostSummary 按模型分组的成本汇总
type ModelCostSummary struct {
	ModelName   string  `json:"model_name"`
	Requests    int64   `json:"requests"`
	TotalTokens int64   `json:"total_tokens"`
	Cost        float64 `json:"cost"`
	AvgCost     float64 `json:"avg_cost"`
}

// GetDashboardSummary 获取仪表盘汇总
func GetDashboardSummary(startTime, endTime time.Time) (*DashboardSummary, error) {
	summary := &DashboardSummary{
		Period: startTime.Format("2006-01-02") + " ~ " + endTime.Format("2006-01-02"),
	}

	// 查询所有 usage logs
	var logs []model.UsageLog
	err := model.DB.Where("created_at BETWEEN ? AND ?", startTime, endTime).
		Find(&logs).Error
	if err != nil {
		return nil, err
	}

	// 统计总 token 数
	var tokenIDs []uint
	model.DB.Model(&model.Token{}).Pluck("id", &tokenIDs)
	summary.TotalTokens = int64(len(tokenIDs))

	// 按套餐和模型分组统计
	planMap := make(map[string]*PlanCostSummary)
	modelMap := make(map[string]*ModelCostSummary)

	for _, log := range logs {
		summary.TotalRequests++
		cost := model.CalculateCost(log.InputTokens, log.OutputTokens, log.Model)
		summary.TotalCost += cost

		// 按套餐分组
		if _, ok := planMap[log.PlanName]; !ok {
			planMap[log.PlanName] = &PlanCostSummary{PlanName: log.PlanName}
		}
		planMap[log.PlanName].Requests++
		planMap[log.PlanName].Cost += cost

		// 按模型分组
		if _, ok := modelMap[log.Model]; !ok {
			modelMap[log.Model] = &ModelCostSummary{ModelName: log.Model}
		}
		modelMap[log.Model].Requests++
		modelMap[log.Model].TotalTokens += log.InputTokens + log.OutputTokens
		modelMap[log.Model].Cost += cost

		// 统计 token 数
		summary.TotalTokens += log.InputTokens + log.OutputTokens
	}

	// 计算套餐收入和利润
	for planName, planSummary := range planMap {
		var plan model.Plan
		model.DB.Where("name = ?", planName).First(&plan)
		if plan.Price != "" {
			var price float64
			fmt.Sscanf(plan.Price, "%f", &price)

			// 统计该套餐的 token 数
			var tokenCount int64
			model.DB.Model(&model.Token{}).Where("rate_limit_group = ?", planName).Count(&tokenCount)
			planSummary.TokenCount = tokenCount
			planSummary.Revenue = price * float64(tokenCount)
			planSummary.Profit = planSummary.Revenue - planSummary.Cost
			if planSummary.Revenue > 0 {
				planSummary.ProfitMargin = (planSummary.Profit / planSummary.Revenue) * 100
			}
		}
		summary.ByPlan = append(summary.ByPlan, *planSummary)
	}

	// 计算模型平均成本
	for _, modelSummary := range modelMap {
		if modelSummary.Requests > 0 {
			modelSummary.AvgCost = modelSummary.Cost / float64(modelSummary.Requests)
		}
		summary.ByModel = append(summary.ByModel, *modelSummary)
	}

	// 计算总收入和利润
	for _, planSummary := range summary.ByPlan {
		summary.TotalRevenue += planSummary.Revenue
	}
	summary.TotalProfit = summary.TotalRevenue - summary.TotalCost
	if summary.TotalRevenue > 0 {
		summary.ProfitMargin = (summary.TotalProfit / summary.TotalRevenue) * 100
	}

	return summary, nil
}

// CheckTokenLoss 检查 Token 是否亏损（成本 > 收入）
func CheckTokenLoss(tokenID uint) (bool, float64, error) {
	// 获取当前月份的统计
	now := time.Now()
	startTime := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	endTime := now

	summary, err := GetTokenCostSummary(tokenID, startTime, endTime)
	if err != nil {
		return false, 0, err
	}

	// 判断是否亏损
	isLoss := summary.TotalCost > summary.Revenue
	return isLoss, summary.Profit, nil
}
