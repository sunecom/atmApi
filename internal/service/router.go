package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"atmapi/internal/model"
)

// 渠道并发控制（通用）
var (
	channelConcurrency = make(map[uint]int) // channelID -> 当前并发数
	concurrencyMutex   sync.Mutex
)

// acquireConcurrency 尝试获取并发槽位
func acquireConcurrency(channelID uint, maxConcurrent int) bool {
	concurrencyMutex.Lock()
	defer concurrencyMutex.Unlock()
	
	current := channelConcurrency[channelID]
	if current >= maxConcurrent {
		return false
	}
	channelConcurrency[channelID] = current + 1
	return true
}

// releaseConcurrency 释放并发槽位
func releaseConcurrency(channelID uint) {
	concurrencyMutex.Lock()
	defer concurrencyMutex.Unlock()
	
	if current, ok := channelConcurrency[channelID]; ok && current > 0 {
		channelConcurrency[channelID] = current - 1
	}
}

// RouteRequestResult 路由请求结果
type RouteRequestResult struct {
	Response    *http.Response
	ChannelName string
	AtmModel    string
	ActualModel string // 实际发给渠道的模型名
}

// ModelRoute 模型路由表
// 对外模型名 → 实际要试的渠道列表（按优先级从高到低）
// 每个条目：[channel_id, model_override, priority]
// 数字越大越优先，失败就 fallback 到下一个
type ModelRouteEntry struct {
	ChannelID uint
	ChannelName string
	ModelOverride string // 空字符串表示用原模型名
	Priority int
}

type ModelRouteConfig struct {
	VisibleModel string // 对外暴露的模型名
	Routes []ModelRouteEntry
}

// modelRouter 路由策略配置（已移除 glm-5.2，改走聚合组 model_group）
// key: 对外模型名, value: 要试的渠道列表（按优先级降序）
var modelRouter = map[string][]ModelRouteEntry{
	// glm-5.2 已迁移到聚合组（channels 表 model_group='glm-5.2'）
	// 聚合组全挂时直接报错，不 fallback 到其他模型

	// deepseek-a4：atm卡 统一模型名
	// 注：smart_router.go 的 SmartRoute() 会先拦截 deepseek-a4
	// 根据消息内容（图片/复杂度）转成具体模型名：qwen3.7-plus / deepseek-v4-flash / deepseek-v4-pro
	// 这里的路由表是兜底，万一 SmartRoute 没有命中（不太可能）
	"deepseek-a4": {
		{ChannelID: 1, ModelOverride: "qwen3.7-plus", Priority: 100},    // 多模态
		{ChannelID: 2, ModelOverride: "deepseek-v4-pro", Priority: 90}, // 深度推理（同 DeepSeek 渠道）
		{ChannelID: 2, ModelOverride: "deepseek-v4-flash", Priority: 80}, // 默认
	},
}

// isFastFailError 判断一个错误是否来自快速失败
func isFastFailError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "快速失败")
}

// RouteRequest 路由请求到合适渠道
func RouteRequest(targetModel string, requestBody []byte, tokenKey string) (*RouteRequestResult, error) {
	// 1. 验证 token
	token, err := validateToken(tokenKey)
	if err != nil {
		return nil, fmt.Errorf("token 验证失败：%w", err)
	}

	// 2. 检查配额（总量）
	if token.RemainQuota == 0 && !token.UnlimitedQuota {
		return nil, fmt.Errorf("token 配额已用完")
	}

	// 3. 检查滑动窗口限流
	rlResult := CheckRateLimit(token)
	if !rlResult.Allowed {
		return nil, fmt.Errorf("限流：%s", rlResult.Reason)
	}

	// 4a. 先查聚合组（model_group）
	if groupChannels, err := getModelGroupChannels(targetModel); err == nil && len(groupChannels) > 0 {
		log.Printf("[路由] 模型 %s 命中聚合组，共 %d 个渠道", targetModel, len(groupChannels))
		result, groupErr := routeToModelGroup(groupChannels, requestBody, token, targetModel)
		if groupErr == nil {
			return result, nil
		}
		// 快速失败错误直接返回，不走后续路径
		if isFastFailError(groupErr) {
			log.Printf("[路由] 聚合组快速失败，终止路由: %v", groupErr)
			return nil, groupErr
		}
		// 普通错误继续 fallback
		log.Printf("[路由] 聚合组全挂，fallback 到路由表: %v", groupErr)
	}

	// 4b. 查路由表（modelRouter）
	if routes, ok := modelRouter[targetModel]; ok {
		log.Printf("[路由] 模型 %s 命中路由表，共 %d 个备选渠道", targetModel, len(routes))
		result, routeErr := routeToBestChannel(targetModel, requestBody, token, routes)
		if routeErr == nil {
			return result, nil
		}
		// 快速失败错误直接返回
		if isFastFailError(routeErr) {
			log.Printf("[路由] 路由表快速失败，终止路由: %v", routeErr)
			return nil, routeErr
		}
		log.Printf("[路由] 路由表失败，fallback 到 LIKE 查询: %v", routeErr)
	}

	// 4c. 原逻辑：查 models LIKE 匹配
	log.Printf("[路由] 查找模型: %s（LIKE 查询）", targetModel)
	channels, err := getChannelsForModel(targetModel)
	if err != nil {
		return nil, fmt.Errorf("获取渠道失败：%w", err)
	}
	log.Printf("[路由] LIKE 查询找到 %d 个渠道", len(channels))

	if len(channels) == 0 {
		return nil, fmt.Errorf("没有可用渠道")
	}

	// 5. 尝试每个渠道（自动 fallback）
	var lastErr error
	for _, channel := range channels {
		// 渠道维度限流（保护特定渠道不被打爆）
		if token.RateLimitGroup != "" {
			plan, planErr := GetPlan(token.RateLimitGroup)
			if planErr == nil && plan.MaxChannelRPM > 0 {
				allowed, currentRPM, maxChannelRPM, retryAfter := GlobalChannelLimiter.CheckChannelRPM(token.ID, channel.Name, plan.MaxChannelRPM)
				if !allowed {
					log.Printf("[路由] 渠道 %s RPM超限（%d/%d），跳过", channel.Name, currentRPM, maxChannelRPM)
					lastErr = fmt.Errorf("渠道 %s RPM超限，请%d秒后再试", channel.Name, retryAfter)
					continue
				}
			}
		}

		resp, actualSentModel, err := trySingleChannel(channel, targetModel, requestBody)
		if err != nil {
			// 快速失败直接返回
			if isFastFailError(err) {
				return nil, err
			}
			lastErr = err
			continue
		}

		// 更新配额
		updateQuota(token, 1)
		RecordRequest(token.ID) // 记录滑动窗口
		GlobalRPMLimiter.RecordRPM(token.ID) // 记录 RPM
		GlobalChannelLimiter.RecordChannelRPM(token.ID, channel.Name) // 记录渠道 RPM

		return &RouteRequestResult{Response: resp, ChannelName: channel.Name, AtmModel: channel.AtmModel, ActualModel: actualSentModel}, nil
	}

	return nil, fmt.Errorf("所有渠道均失败：%w", lastErr)
}

// getModelGroupChannels 查询聚合组渠道
func getModelGroupChannels(modelName string) ([]model.Channel, error) {
	var channels []model.Channel
	err := model.DB.Where(
		"model_group = ? AND status = ?",
		modelName, 1,
	).Order("priority DESC").Find(&channels).Error
	return channels, err
}

// routeToModelGroup 聚合组路由（纯模型一致性，全挂报错）
func routeToModelGroup(channels []model.Channel, requestBody []byte, token *model.Token, targetModel string) (*RouteRequestResult, error) {
	var lastErr error

	for _, channel := range channels {
		// 并发控制
		acquired := false
		if channel.MaxConcurrent > 0 {
			if !acquireConcurrency(channel.ID, channel.MaxConcurrent) {
				log.Printf("[路由] 渠道 %s 并发已满(%d)，跳过", channel.Name, channel.MaxConcurrent)
				continue
			}
			acquired = true
		}

		log.Printf("[路由] 聚合组尝试: %s (Priority=%d)", channel.Name, channel.Priority)
		// BUG-002 修复：传 targetModel 而不是 channel.Models
		resp, actualSentModel, err := trySingleChannel(channel, targetModel, requestBody)
		if err != nil {
			// BUG-005 修复：失败时立即释放并发槽位
			if acquired {
				releaseConcurrency(channel.ID)
			}
			lastErr = err
			continue
		}

		// 成功：先释放槽位，再返回
		if acquired {
			releaseConcurrency(channel.ID)
		}
		updateQuota(token, 1)
		RecordRequest(token.ID) // 记录滑动窗口
		return &RouteRequestResult{Response: resp, ChannelName: channel.Name, AtmModel: channel.AtmModel, ActualModel: actualSentModel}, nil
	}

	// 聚合组全挂 → 直接报错（不偷偷换模型）
	return nil, fmt.Errorf("聚合组 [%s] 所有渠道均失败：%w", channels[0].ModelGroup, lastErr)
}

// routeToBestChannel 按路由策略逐个尝试渠道
func routeToBestChannel(originalModel string, requestBody []byte, token *model.Token, routes []ModelRouteEntry) (*RouteRequestResult, error) {
	var lastErr error

	for _, entry := range routes {
		// 从数据库查渠道
		var channel model.Channel
		result := model.DB.First(&channel, entry.ChannelID)
		if result.Error != nil || channel.Status != 1 {
			log.Printf("[路由] 渠道 %s (ID=%d) 不可用，跳过", entry.ChannelName, entry.ChannelID)
			continue
		}

		// 通用并发控制
		acquired := false
		if channel.MaxConcurrent > 0 {
			if !acquireConcurrency(channel.ID, channel.MaxConcurrent) {
				log.Printf("[路由] 渠道 %s 并发已满(%d)，跳过", channel.Name, channel.MaxConcurrent)
				continue
			}
			acquired = true
		}

		log.Printf("[路由] 尝试: %s → %s", entry.ChannelName, entry.ModelOverride)
		resp, actualSentModel, err := trySingleChannel(channel, entry.ModelOverride, requestBody)
		
		if err != nil {
			// BUG-005 修复：失败时立即释放并发槽位
			if acquired {
				releaseConcurrency(channel.ID)
			}
			lastErr = err
			continue
		}

		// 成功：先释放槽位，再返回
		if acquired {
			releaseConcurrency(channel.ID)
		}
		updateQuota(token, 1)
		RecordRequest(token.ID) // 记录滑动窗口
		return &RouteRequestResult{Response: resp, ChannelName: channel.Name, AtmModel: channel.AtmModel, ActualModel: actualSentModel}, nil
	}

	return nil, fmt.Errorf("路由策略无可用渠道：%w", lastErr)
}

// trySingleChannel 尝试单个渠道，返回响应或错误
// shouldFastFail 判断上游返回的状态码是否应该快速失败（不继续 fallback）
// 4xx 客户端错误通常是消息格式问题或模型不存在，fallback 到其他渠道也一样
// 5xx 服务端错误可以继续尝试其他渠道
func shouldFastFail(statusCode int) bool {
	// 400 + 401 + 403 = 客户端问题，无需重试
	// 404 = 渠道端点不存在，不用重试
	if statusCode >= 400 && statusCode < 500 {
		// 429 限流可以重试其他渠道
		if statusCode == 429 {
			return false
		}
		return true
	}
	return false
}

func trySingleChannel(channel model.Channel, targetModel string, originalBody []byte) (*http.Response, string, error) {
	if channel.Status != 1 {
		return nil, "", fmt.Errorf("渠道 %s 未启用", channel.Name)
	}

	// 模型名替换：如果有 model_mapping 先用他，否则用目标模型名
	mappedModel := applyModelMapping(channel.ModelMapping, targetModel)
	limitedBody := limitTokenUsage(originalBody)
	// 压缩大图（减少 coding 端点超时）
	compressedBody := compressImagesInBody(limitedBody)
	modifiedBody := replaceModelInRequest(compressedBody, mappedModel)

	resp, err := sendToChannel(channel, modifiedBody)
	if err != nil {
		log.Printf("渠道 %s 失败：%v", channel.Name, err)
		return nil, mappedModel, fmt.Errorf("渠道 %s 请求失败：%w", channel.Name, err)
	}

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		log.Printf("渠道 %s 返回 HTTP %d: %s", channel.Name, resp.StatusCode, string(bodyBytes))
		
		if shouldFastFail(resp.StatusCode) {
			// 4xx 客户端错误 → 快速失败，不继续 fallback
			// 消息格式问题（如 tool_calls 不匹配）换个渠道也一样，不要浪费时间
			return nil, mappedModel, fmt.Errorf("渠道 %s 返回 HTTP %d（快速失败）: %s",
				channel.Name, resp.StatusCode, string(bodyBytes))
		}
		return nil, mappedModel, fmt.Errorf("渠道 %s 返回 HTTP %d", channel.Name, resp.StatusCode)
	}

	return resp, mappedModel, nil
}

// TestChannel 测试单个渠道连通性
func TestChannel(channel model.Channel) (int, error) {
	testBody := []byte(`{"model":"test","messages":[{"role":"user","content":"hi"}],"max_tokens":1}`)
	url := channel.BaseURL + "/v1/chat/completions"

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(testBody))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+channel.Key)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// validateToken 验证 token
func validateToken(key string) (*model.Token, error) {
	tk, err := model.FindByKey(key)
	if err != nil {
		return nil, fmt.Errorf("token 不存在或已禁用")
	}

	// 首次使用激活逻辑：所有 token 首次使用都设置过期时间
	if tk.ActivatedAt == 0 {
		now := time.Now()
		tk.ActivatedAt = now.Unix()
		// 过期时间 = 激活时刻 + 1个自然月
		tk.ExpiredTime = now.AddDate(0, 1, 0).Unix()
		model.DB.Save(tk)
		log.Printf("[激活] token %s 首次使用，激活时间=%s，过期时间=%s", 
			tk.Name, 
			now.Format("2006-01-02 15:04:05"),
			now.AddDate(0, 1, 0).Format("2006-01-02 15:04:05"))
	}

	if tk.ExpiredTime > 0 && time.Now().Unix() > tk.ExpiredTime {
		tk.Status = 3
		model.DB.Save(tk)
		return nil, fmt.Errorf("token 已过期")
	}

	return tk, nil
}

// getChannelsForModel 获取支持指定模型的渠道列表
func getChannelsForModel(modelName string) ([]model.Channel, error) {
	// 统一转小写，兼容不同大小写写法的模型名
	modelName = strings.ToLower(modelName)
	
	var channels []model.Channel
	result := model.DB.Where(
		"LOWER(models) LIKE ? AND status = ?",
		"%"+modelName+"%",
		1,
	).Order("priority DESC, weight DESC").Find(&channels)

	if result.Error != nil {
		return nil, result.Error
	}
	return channels, nil
}

// applyModelMapping 应用模型映射
func applyModelMapping(mappingJSON string, originalModel string) string {
	if mappingJSON == "" {
		return originalModel
	}
	var mapping map[string]string
	if err := json.Unmarshal([]byte(mappingJSON), &mapping); err != nil {
		return originalModel
	}
	if mapped, ok := mapping[originalModel]; ok {
		return mapped
	}
	return originalModel
}

// limitTokenUsage 限制每次调用的 token 消耗（控制成本核心手段）
// 2026-07-08 修复：不再裁剪历史消息，交给 OpenClaw compaction 管理上下文
// 原逻辑：只保留 system + 最近 3 轮（最多 7 条）→ 导致"失忆"
func limitTokenUsage(body []byte) []byte {
	// 不再裁剪历史，直接返回原始 body
	// 上下文管理交给客户端（OpenClaw compaction）
	return body
}

// replaceModelInRequest 替换请求体中的模型名
func replaceModelInRequest(body []byte, newModel string) []byte {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}
	req["model"] = newModel
	newBody, _ := json.Marshal(req)
	return newBody
}

// sendToChannel 发送请求到指定渠道
func sendToChannel(channel model.Channel, body []byte) (*http.Response, error) {
	url := channel.BaseURL
	// 如果 base_url 不以 /chat/completions 结尾，才追加 /v1/chat/completions
	if !strings.HasSuffix(url, "/chat/completions") {
		url += "/v1/chat/completions"
	}
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+channel.Key)
	// 图片请求需要更长时间（qwen3.7-plus 处理大图可能要 60-90 秒）
	timeout := 30 * time.Second
	if strings.Contains(string(body), "image_url") || strings.Contains(string(body), "data:image") {
		timeout = 90 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	return client.Do(req)
}

// updateQuota 更新配额
func updateQuota(token *model.Token, count int64) {
	if !token.UnlimitedQuota && token.RemainQuota > 0 {
		token.RemainQuota -= count
	}
	token.AccessedTime = time.Now().Unix()
	model.DB.Save(token)
}

// SortChannels 排序渠道
func SortChannels(channels []model.Channel) {
	sort.Slice(channels, func(i, j int) bool {
		if channels[i].Priority != channels[j].Priority {
			return channels[i].Priority > channels[j].Priority
		}
		return channels[i].Weight > channels[j].Weight
	})
}

// ReadBody 读取请求体
func ReadBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewBuffer(body))
	return body, nil
}
