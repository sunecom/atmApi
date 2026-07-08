package service

import (
	"fmt"
	"log"
	"sync"
	"time"

	"atmapi/internal/model"
)

// GetPlan 获取套餐配置
func GetPlan(planName string) (*model.Plan, error) {
	var plan model.Plan
	err := model.DB.Where("name = ?", planName).First(&plan).Error
	if err != nil {
		return nil, err
	}
	return &plan, nil
}

// RateLimitResult 限流检查结果
type RateLimitResult struct {
	Allowed      bool
	Reason       string  // 不允许时的原因
	RetryAfter   int64   // 建议重试等待秒数
	Used5h       int64
	Limit5h      int64
	UsedDaily    int64
	LimitDaily   int64
	UsedWeekly   int64
	LimitWeekly  int64
	UsedMonthly  int64
	LimitMonthly int64
	UsedImages   int64   // 今日图片次数
	LimitImages  int64   // 每日图片上限
}

// ConcurrencyLimiter 并发限制器（内存级）
var ConcurrencyLimiter = &concurrencyLimiter{
	current: make(map[uint]int),
	mu:      sync.Mutex{},
}

type concurrencyLimiter struct {
	current map[uint]int // tokenID -> 当前并发数
	mu      sync.Mutex
}

// Acquire 尝试获取并发槽位，返回 (成功, 当前并发数, 上限)
func (cl *concurrencyLimiter) Acquire(tokenID uint, maxQPS int64) (bool, int, int64) {
	if maxQPS <= 0 {
		return true, 0, maxQPS
	}
	cl.mu.Lock()
	defer cl.mu.Unlock()
	current := cl.current[tokenID]
	if int64(current) >= maxQPS {
		return false, current, maxQPS
	}
	cl.current[tokenID] = current + 1
	return true, current + 1, maxQPS
}

// Release 释放并发槽位
func (cl *concurrencyLimiter) Release(tokenID uint) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if c, ok := cl.current[tokenID]; ok && c > 0 {
		cl.current[tokenID] = c - 1
	}
}

// CheckRateLimit 滑动窗口限流检查（增强版）
// 检查顺序：5小时窗口 → 每日窗口 → 每周窗口 → 每月窗口 → 图片次数
func CheckRateLimit(token *model.Token) *RateLimitResult {
	result := &RateLimitResult{Allowed: true}

	if token.RateLimitGroup == "" {
		return result
	}

	plan, err := GetPlan(token.RateLimitGroup)
	if err != nil {
		log.Printf("[限流] token %s 的限流组 %s 未配置，跳过限流", token.Key, token.RateLimitGroup)
		return result
	}

	now := time.Now().Unix()
	result.Limit5h = plan.Hourly5Max
	result.LimitDaily = plan.DailyMax
	result.LimitWeekly = plan.WeeklyMax
	result.LimitMonthly = plan.MonthlyMax
	result.LimitImages = plan.DailyImageMax

	// 1. 5小时滚动窗口检查
	if !plan.SkipHourly && plan.Hourly5Max > 0 {
		window5hStart := now - 5*3600
		model.DB.Model(&model.RateLimit{}).
			Where("token_id = ? AND request_time > ?", token.ID, window5hStart).
			Count(&result.Used5h)

		if result.Used5h >= plan.Hourly5Max {
			result.Allowed = false
			result.Reason = fmt.Sprintf("5小时配额已用完（%d/%d），请稍后再试", result.Used5h, plan.Hourly5Max)
			// 计算最早一条记录距今多久，估算 retry_after
			var oldestInWindow model.RateLimit
			if err := model.DB.Where("token_id = ? AND request_time > ?", token.ID, window5hStart).
				Order("request_time ASC").First(&oldestInWindow).Error; err == nil {
				elapsed := now - oldestInWindow.RequestTime
				result.RetryAfter = 5*3600 - elapsed + 1
				if result.RetryAfter < 1 {
					result.RetryAfter = 1
				}
			}
			return result
		}
	}

	// 2. 每日窗口检查
	if plan.DailyMax > 0 {
		dailyStart := now - 24*3600
		model.DB.Model(&model.RateLimit{}).
			Where("token_id = ? AND request_time > ?", token.ID, dailyStart).
			Count(&result.UsedDaily)

		if result.UsedDaily >= plan.DailyMax {
			result.Allowed = false
			result.Reason = fmt.Sprintf("每日配额已用完（%d/%d），请明天再试", result.UsedDaily, plan.DailyMax)
			result.RetryAfter = 24 * 3600
			return result
		}
	}

	// 3. 每周窗口检查
	if plan.WeeklyMax > 0 {
		weekStart := now - 7*24*3600
		model.DB.Model(&model.RateLimit{}).
			Where("token_id = ? AND request_time > ?", token.ID, weekStart).
			Count(&result.UsedWeekly)

		if result.UsedWeekly >= plan.WeeklyMax {
			result.Allowed = false
			result.Reason = fmt.Sprintf("每周配额已用完（%d/%d），请下周再试", result.UsedWeekly, plan.WeeklyMax)
			result.RetryAfter = 7 * 24 * 3600
			return result
		}
	}

	// 4. 每月窗口检查（30天硬上限）
	if plan.MonthlyMax > 0 {
		monthStart := now - 30*24*3600
		model.DB.Model(&model.RateLimit{}).
			Where("token_id = ? AND request_time > ?", token.ID, monthStart).
			Count(&result.UsedMonthly)

		if result.UsedMonthly >= plan.MonthlyMax {
			result.Allowed = false
			result.Reason = fmt.Sprintf("每月配额已用完（%d/%d），请下月再试", result.UsedMonthly, plan.MonthlyMax)
			result.RetryAfter = 30 * 24 * 3600
			return result
		}
	}

	// 5. 每日图片次数检查
	if plan.DailyImageMax > 0 {
		dailyStart := now - 24*3600
		model.DB.Model(&model.ImageUsage{}).
			Where("token_id = ? AND created_at > ?", token.ID, time.Unix(dailyStart, 0)).
			Count(&result.UsedImages)

		if result.UsedImages >= plan.DailyImageMax {
			result.Allowed = false
			result.Reason = fmt.Sprintf("每日图片次数已用完（%d/%d），请明天再试", result.UsedImages, plan.DailyImageMax)
			result.RetryAfter = 24 * 3600
			return result
		}
	}

	return result
}

// RecordRequest 记录一次请求（用于滑动窗口计数）
func RecordRequest(tokenID uint) {
	rateLimit := model.RateLimit{
		TokenID:     tokenID,
		RequestTime: time.Now().Unix(),
	}
	model.DB.Create(&rateLimit)
}

// RecordImageUsage 记录一次图片使用（用于每日图片次数限制）
func RecordImageUsage(tokenID uint) {
	usage := model.ImageUsage{
		TokenID: tokenID,
	}
	model.DB.Create(&usage)
	log.Printf("[限流] 记录图片使用: tokenID=%d", tokenID)
}

// CheckInputTokenLimit 检查单次输入Token是否超限
// 返回 (是否允许, 限制值, 实际估算值)
func CheckInputTokenLimit(token *model.Token, estimatedInputTokens int) (bool, int, int) {
	if token.RateLimitGroup == "" {
		return true, 0, estimatedInputTokens
	}

	plan, err := GetPlan(token.RateLimitGroup)
	if err != nil {
		return true, 0, estimatedInputTokens
	}

	if plan.MaxInputTokens <= 0 {
		return true, 0, estimatedInputTokens
	}

	if estimatedInputTokens > plan.MaxInputTokens {
		log.Printf("[限流] 输入Token超限: tokenID=%d, 估算=%d, 上限=%d",
			token.ID, estimatedInputTokens, plan.MaxInputTokens)
		return false, plan.MaxInputTokens, estimatedInputTokens
	}

	return true, plan.MaxInputTokens, estimatedInputTokens
}

// EstimateInputTokens 估算请求的输入Token数
// 简单启发式：按字符数/4估算（中英文混合场景下偏保守）
func EstimateInputTokens(messages []map[string]interface{}) int {
	totalChars := 0
	for _, msg := range messages {
		content, ok := msg["content"]
		if !ok {
			continue
		}
		switch v := content.(type) {
		case string:
			totalChars += len(v)
		case []interface{}:
			// 多模态格式（含图片）
			for _, part := range v {
				if partMap, ok := part.(map[string]interface{}); ok {
					if text, ok := partMap["text"].(string); ok {
						totalChars += len(text)
					}
					// 图片类型按固定Token开销计算（约85 tokens/张）
					if typ, ok := partMap["type"].(string); ok && typ == "image_url" {
						totalChars += 340 // 85 tokens * 4 chars/token
					}
				}
			}
		}
	}
	// 按字符数/4估算Token数（偏保守）
	return totalChars / 4
}

// CleanOldRecords 清理过期记录（超过31天的）
func CleanOldRecords() {
	threshold := time.Now().Add(-31 * 24 * time.Hour).Unix()
	model.DB.Where("request_time < ?", threshold).Delete(&model.RateLimit{})
	// 清理31天前的图片使用记录
	model.DB.Where("created_at < ?", time.Now().Add(-31*24*time.Hour)).Delete(&model.ImageUsage{})
	log.Printf("[限流] 清理31天前的限流记录和图片使用记录完成")
}

// ===== RPM 限流（每分钟请求数）=====
// 内存级滑动窗口，防止突发打爆

// RPMLimiter 每分钟请求数限制器
type RPMLimiter struct {
	mu       sync.Mutex
	requests map[uint][]int64 // tokenID -> 请求时间戳列表
}

var GlobalRPMLimiter = &RPMLimiter{
	requests: make(map[uint][]int64),
}

// CheckRPM 检查 RPM 是否超限
// 返回 (是否允许, 当前RPM, 上限, 建议等待秒数)
func (r *RPMLimiter) CheckRPM(tokenID uint, maxRPM int64) (bool, int64, int64, int64) {
	if maxRPM <= 0 {
		return true, 0, maxRPM, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().Unix()
	windowStart := now - 60 // 1分钟窗口

	// 清理过期记录
	if reqs, ok := r.requests[tokenID]; ok {
		validReqs := []int64{}
		for _, ts := range reqs {
			if ts > windowStart {
				validReqs = append(validReqs, ts)
			}
		}
		r.requests[tokenID] = validReqs
	}

	currentRPM := int64(len(r.requests[tokenID]))
	if currentRPM >= maxRPM {
		// 计算最早一条记录距今多久
		var retryAfter int64 = 60
		if len(r.requests[tokenID]) > 0 {
			oldest := r.requests[tokenID][0]
			retryAfter = 60 - (now - oldest) + 1
			if retryAfter < 1 {
				retryAfter = 1
			}
		}
		return false, currentRPM, maxRPM, retryAfter
	}

	return true, currentRPM, maxRPM, 0
}

// RecordRPM 记录一次请求
func (r *RPMLimiter) RecordRPM(tokenID uint) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().Unix()
	r.requests[tokenID] = append(r.requests[tokenID], now)
}

// CleanRPM 清理过期记录（定期调用）
func (r *RPMLimiter) CleanRPM() {
	r.mu.Lock()
	defer r.mu.Unlock()

	windowStart := time.Now().Unix() - 60
	for tokenID, reqs := range r.requests {
		validReqs := []int64{}
		for _, ts := range reqs {
			if ts > windowStart {
				validReqs = append(validReqs, ts)
			}
		}
		if len(validReqs) == 0 {
			delete(r.requests, tokenID)
		} else {
			r.requests[tokenID] = validReqs
		}
	}
}

// ===== 渠道维度限流 =====
// 保护特定渠道不被打爆（如 qwen3.7 图片请求）

// ChannelLimiter 渠道维度限流器
type ChannelLimiter struct {
	mu       sync.Mutex
	requests map[string][]int64 // "tokenID:channelName" -> 请求时间戳列表
}

var GlobalChannelLimiter = &ChannelLimiter{
	requests: make(map[string][]int64),
}

// CheckChannelRPM 检查渠道维度 RPM 是否超限
// 返回 (是否允许, 当前RPM, 上限, 建议等待秒数)
func (c *ChannelLimiter) CheckChannelRPM(tokenID uint, channelName string, maxChannelRPM int64) (bool, int64, int64, int64) {
	if maxChannelRPM <= 0 {
		return true, 0, maxChannelRPM, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	key := fmt.Sprintf("%d:%s", tokenID, channelName)
	now := time.Now().Unix()
	windowStart := now - 60

	// 清理过期记录
	if reqs, ok := c.requests[key]; ok {
		validReqs := []int64{}
		for _, ts := range reqs {
			if ts > windowStart {
				validReqs = append(validReqs, ts)
			}
		}
		c.requests[key] = validReqs
	}

	currentRPM := int64(len(c.requests[key]))
	if currentRPM >= maxChannelRPM {
		var retryAfter int64 = 60
		if len(c.requests[key]) > 0 {
			oldest := c.requests[key][0]
			retryAfter = 60 - (now - oldest) + 1
			if retryAfter < 1 {
				retryAfter = 1
			}
		}
		return false, currentRPM, maxChannelRPM, retryAfter
	}

	return true, currentRPM, maxChannelRPM, 0
}

// RecordChannelRPM 记录一次渠道请求
func (c *ChannelLimiter) RecordChannelRPM(tokenID uint, channelName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := fmt.Sprintf("%d:%s", tokenID, channelName)
	now := time.Now().Unix()
	c.requests[key] = append(c.requests[key], now)
}

// CleanChannelRPM 清理过期记录
func (c *ChannelLimiter) CleanChannelRPM() {
	c.mu.Lock()
	defer c.mu.Unlock()

	windowStart := time.Now().Unix() - 60
	for key, reqs := range c.requests {
		validReqs := []int64{}
		for _, ts := range reqs {
			if ts > windowStart {
				validReqs = append(validReqs, ts)
			}
		}
		if len(validReqs) == 0 {
			delete(c.requests, key)
		} else {
			c.requests[key] = validReqs
		}
	}
}
