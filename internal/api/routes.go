package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"atmapi/internal/middleware"
	"atmapi/internal/model"
	"atmapi/internal/service"

	"github.com/gin-gonic/gin"
)

// ===== 标准化错误码体系 =====

type ErrorCode string

const (
	ErrInvalidRequest    ErrorCode = "INVALID_REQUEST"
	ErrUnauthorized      ErrorCode = "UNAUTHORIZED"
	ErrTokenExpired      ErrorCode = "TOKEN_EXPIRED"
	ErrTokenDisabled     ErrorCode = "TOKEN_DISABLED"
	ErrTokenNotFound     ErrorCode = "TOKEN_NOT_FOUND"
	ErrRateLimitExceeded ErrorCode = "RATE_LIMIT_EXCEEDED"
	ErrModelNotFound     ErrorCode = "MODEL_NOT_FOUND"
	ErrChannelUnavail    ErrorCode = "CHANNEL_UNAVAILABLE"
	ErrImageTooLarge     ErrorCode = "IMAGE_TOO_LARGE"
	ErrInternal          ErrorCode = "INTERNAL_ERROR"
	ErrPaymentRequired   ErrorCode = "PAYMENT_REQUIRED"
	ErrOrderNotFound     ErrorCode = "ORDER_NOT_FOUND"
)

// APIError 标准化错误响应
type APIError struct {
	Code    ErrorCode   `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// respondError 统一错误响应格式
func respondError(c *gin.Context, httpStatus int, code ErrorCode, message string, details ...interface{}) {
	errResp := APIError{
		Code:    code,
		Message: message,
	}
	if len(details) > 0 {
		errResp.Details = details[0]
	}
	c.JSON(httpStatus, gin.H{"error": errResp})
}

// RegisterRoutes 注册所有路由
func RegisterRoutes(r *gin.Engine) {
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	r.Static("/static", "./web/static")
	r.GET("/", indexPage)
	r.GET("/health", healthCheck)
	r.GET("/cache/stats", cacheStats)
	r.GET("/token-info", func(c *gin.Context) { c.Redirect(301, "/static/token-info.html") })

	// OpenAI 兼容路由（/v1）— 给 OpenClaw Gateway 和标准客户端用
	oai := r.Group("/v1")
	{
		oai.POST("/chat/completions", chatCompletions)
		oai.GET("/models", listModels)
	}

	v1 := r.Group("/api/v1")
	{
		v1.POST("/login", login)
		v1.POST("/chat/completions", chatCompletions)
		v1.POST("/register", register)
		v1.GET("/models", listModels)
		v1.GET("/token-info", tokenInfo) // 客户查询 token 信息

		managed := v1.Group("")
		managed.Use(middleware.AuthRequired())
		{
			managed.GET("/tokens", getTokens)
			managed.POST("/tokens", createToken)
			managed.PUT("/tokens/:id", updateToken)
			managed.DELETE("/tokens/:id", deleteToken)

			managed.GET("/channels", getChannels)
			managed.POST("/channels", createChannel)
			managed.PUT("/channels/:id", updateChannel)
			managed.DELETE("/channels/:id", deleteChannel)
			managed.POST("/channels/:id/test", testChannel)

			managed.GET("/logs", getLogs)
			managed.GET("/usage", getUsageStats)
			managed.GET("/settings", getSystemSettings)
			managed.GET("/logs/export", exportLogs)

			// 成本分析 API
			managed.GET("/cost-summary", getCostSummary)
			managed.GET("/cost-by-plan", getCostByPlan)
			managed.GET("/cost-trend", getCostTrend)
		}

		admin := v1.Group("")
		admin.Use(middleware.AuthRequired())
		admin.Use(middleware.AdminRequired())
		{
			admin.GET("/users", getUsers)
			admin.POST("/users", createUser)
			admin.PUT("/users/:id", updateUser)
			admin.DELETE("/users/:id", deleteUser)

			// 支付管理
			admin.GET("/payments", getPayments)
			admin.POST("/payments/refund", refundPayment)
		}

		// ===== 支付相关路由（公网）=====
		payment := v1.Group("/payment")
		{
			// 1. 用户发起购买 → 创建订单 + 返回支付链接
			payment.POST("/create-order", createOrder)
			// 2. 支付宝异步回调（阿里主动 POST 过来）
			payment.POST("/alipay-notify", alipayNotify)
			// 3. 微信异步回调（腾讯主动 POST 过来）
			payment.POST("/wechat-notify", wechatNotify)
			// 4. 查询订单状态（用户查看支付是否成功）
			payment.GET("/order-status", getOrderStatus)
		}
	}
}

func indexPage(c *gin.Context)     { c.File("./web/static/index.html") }

func healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "version": "2.0.1", "time": time.Now().Format("2006-01-02 15:04:05")})
}

func cacheStats(c *gin.Context) {
	stats := service.GetCacheStats()
	c.JSON(http.StatusOK, gin.H{"data": stats})
}

// ===== 登录认证 =====

func login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var user model.User
	if err := model.DB.Where("username = ? AND password = ? AND status = ?", req.Username, req.Password, 1).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	token, err := middleware.GenerateToken(user.ID, user.Username, user.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成 token 失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "登录成功", "token": token,
		"user_id": user.ID, "username": user.Username,
		"display_name": user.DisplayName, "role": user.Role,
	})
}

func register(c *gin.Context) {
	var req struct {
		Username    string `json:"username" binding:"required"`
		Password    string `json:"password" binding:"required,min=6"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var count int64
	model.DB.Model(&model.User{}).Where("username = ?", req.Username).Count(&count)
	if count > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "用户名已存在"})
		return
	}
	user := model.User{
		Username: req.Username, Password: req.Password,
		DisplayName: req.DisplayName, Email: req.Email,
		Role: 1, Status: 1, Quota: 100000,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "注册失败"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "注册成功", "user_id": user.ID})
}

// ===== Token 管理 =====

func getTokens(c *gin.Context) {
	var tokens []model.Token
	model.DB.Find(&tokens)
	// 脱敏：只显示 key 前 8 位
	for i := range tokens {
		if len(tokens[i].Key) > 8 {
			tokens[i].Key = tokens[i].Key[:8] + "..."
		}
	}
	c.JSON(http.StatusOK, gin.H{"data": tokens})
}

func createToken(c *gin.Context) {
	var req struct {
		UserID           uint   `json:"user_id" binding:"required"`
		Name             string `json:"name" binding:"required"`
		RemainQuota      int64  `json:"remain_quota"`
		UnlimitedQuota   bool   `json:"unlimited_quota"`
		ExpiredTime      int64  `json:"expired_time"`
		RateLimitGroup   string `json:"rate_limit_group"`   // BUG-003 修复：支持设置限流组
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	// 月卡套餐自动设置无限配额（靠滑动窗口限速率）
	if req.RateLimitGroup != "" {
		// 从 plans 表验证套餐是否存在
		var plan model.Plan
		if err := model.DB.Where("name = ?", req.RateLimitGroup).First(&plan).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("套餐 %s 不存在", req.RateLimitGroup)})
			return
		}
		// 自动设置无限配额
		req.UnlimitedQuota = true
		req.RemainQuota = -1
	}
	
	token := model.Token{
		UserID: req.UserID, Name: req.Name, Key: generateTokenKey(),
		Status: 1, RemainQuota: req.RemainQuota,
		UnlimitedQuota: req.UnlimitedQuota, ExpiredTime: req.ExpiredTime,
		RateLimitGroup: req.RateLimitGroup, // BUG-003 修复
		CreatedTime: time.Now().Unix(),
	}
	if err := model.DB.Create(&token).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "Token 创建成功", "token": token})
}

func updateToken(c *gin.Context) {
	id := c.Param("id")
	var token model.Token
	if err := model.DB.First(&token, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Token 不存在"})
		return
	}
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 手动处理零值字段，避免 GORM 忽略
	if v, ok := req["remain_quota"]; ok {
		if f, ok := v.(float64); ok {
			token.RemainQuota = int64(f)
		}
		delete(req, "remain_quota")
	}
	if v, ok := req["status"]; ok {
		if f, ok := v.(float64); ok {
			token.Status = int(f)
		}
		delete(req, "status")
	}
	if err := model.DB.Model(&token).Updates(req).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "更新成功", "token": token})
}

func deleteToken(c *gin.Context) {
	id := c.Param("id")
	if err := model.DB.Delete(&model.Token{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}

// ===== 渠道管理 =====

func getChannels(c *gin.Context) {
	var channels []model.Channel
	model.DB.Order("priority DESC, weight DESC").Find(&channels)
	c.JSON(http.StatusOK, gin.H{"data": channels})
}

func createChannel(c *gin.Context) {
	var req model.Channel
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := model.DB.Create(&req).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "渠道创建成功", "channel": req})
}

func updateChannel(c *gin.Context) {
	id := c.Param("id")
	var channel model.Channel
	if err := model.DB.First(&channel, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "渠道不存在"})
		return
	}
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := model.DB.Model(&channel).Updates(req).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "更新成功", "channel": channel})
}

func deleteChannel(c *gin.Context) {
	id := c.Param("id")
	if err := model.DB.Delete(&model.Channel{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}

func testChannel(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效 ID"})
		return
	}
	var channel model.Channel
	if err := model.DB.First(&channel, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "渠道不存在"})
		return
	}
	startTime := time.Now()
	statusCode, err := service.TestChannel(channel)
	duration := time.Since(startTime).Milliseconds()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "channel": channel.Name, "error": err.Error(), "duration_ms": duration})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "channel": channel.Name, "status_code": statusCode, "duration_ms": duration})
}

// ===== 模型路由（核心功能） =====

func chatCompletions(c *gin.Context) {
	startTime := time.Now()
	tokenKey := extractToken(c)
	if tokenKey == "" {
		respondError(c, http.StatusUnauthorized, ErrUnauthorized, "缺少认证 token")
		return
	}
	var apiToken model.Token
	model.DB.Where("key = ?", tokenKey).First(&apiToken)
	
	// 验证 token 是否有效（在图片缓存逻辑之前）
	if apiToken.ID == 0 {
		respondError(c, http.StatusUnauthorized, ErrTokenNotFound, "token 不存在或已禁用")
		return
	}
	
	// 限流检查
	if allowed, reason := service.CheckRateLimit(&apiToken); !allowed {
		respondError(c, http.StatusTooManyRequests, ErrRateLimitExceeded, reason)
		return
	}
	
	body, err := service.ReadBody(c.Request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": ErrInvalidRequest, "message": "读取请求失败"}})
		return
	}
	var req struct {
		Model    string                   `json:"model"`
		Messages []map[string]interface{} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest, "请求格式错误")
		return
	}

	// ===== 图片缓存处理（deepseek-a4 专属）=====
	// 核心逻辑：纯图片 → 缓存+返回模拟响应；后续请求 → 检查缓存+注入图片
	if strings.ToLower(req.Model) == "deepseek-a4" && service.GlobalImageCache != nil {
		hasImage := service.HasImageContent(req.Messages)
		
		if hasImage {
			// 检查是否有实质性问题
			userText := ""
			for _, msg := range req.Messages {
				if role, _ := msg["role"].(string); role == "user" {
					content := msg["content"]
					switch c := content.(type) {
					case string:
						if len(c) > 5 {
							userText = c
						}
					case []interface{}:
						// 多模态格式，提取文字部分
						for _, part := range c {
							if partMap, ok := part.(map[string]interface{}); ok {
								if typ, _ := partMap["type"].(string); typ == "text" {
									if text, ok := partMap["text"].(string); ok && len(text) > 5 {
										userText = text
									}
								}
							}
						}
					}
				}
			}
			
			if userText == "" {
				// 纯图片，没有问题 → 缓存图片 + 返回模拟响应
				stored := service.GlobalImageCache.Store(tokenKey, req.Messages)
				if stored {
					log.Printf("[图片缓存] 纯图片已缓存: token=%s", tokenKey[:min(8, len(tokenKey))])
					c.Data(200, "application/json", service.GenerateImageCacheResponse())
					return
				}
				// 图片过大，返回错误
				respondError(c, http.StatusBadRequest, ErrImageTooLarge, "图片超过 10MB 限制")
				return
			}
			
			// 图片+问题 → 直接用当前图片路由，不取旧缓存（避免新旧图冲突）
			log.Printf("[图片缓存] 图片+问题，直接路由: token=%s", tokenKey[:min(8, len(tokenKey))])
		} else {
			// 纯文字请求 → 检查是否有缓存的图片
			if cached := service.GlobalImageCache.Retrieve(tokenKey); cached != nil {
				// 有缓存图片 → 注入到 messages 中
				mergedMessages := service.MergeImageWithQuestion(cached.Messages, req.Messages)
				req.Messages = mergedMessages
				// 重新序列化 body
				var reqMapNew map[string]interface{}
				json.Unmarshal(body, &reqMapNew)
				reqMapNew["messages"] = mergedMessages
				body, _ = json.Marshal(reqMapNew)
				// 清除缓存（已消费）
				service.GlobalImageCache.Clear(tokenKey)
				log.Printf("[图片缓存] 纯文字请求，注入缓存图片: token=%s", tokenKey[:min(8, len(tokenKey))])
			}
		}
	}

	// 智能路由：根据请求复杂度选择模型
	actualModel := service.SmartRoute(req.Model, req.Messages)
	if actualModel != req.Model {
		// 替换请求体中的 model
		var reqMap map[string]interface{}
		json.Unmarshal(body, &reqMap)
		reqMap["model"] = actualModel
		body, _ = json.Marshal(reqMap)
	}

	// 检查缓存（只对非流式请求）
	var isStream bool
	var reqMap map[string]interface{}
	json.Unmarshal(body, &reqMap)
	if streamVal, ok := reqMap["stream"]; ok {
		isStream, _ = streamVal.(bool)
	}
	
	var cacheKey string
	if !isStream && service.GlobalCache != nil {
		cacheKey = service.GlobalCache.GenerateKey(tokenKey, actualModel, req.Messages)
		if cached, found := service.GlobalCache.Get(cacheKey); found {
			duration := time.Since(startTime).Milliseconds()
			model.DB.Create(&model.RequestLog{
				TokenName: apiToken.Name, ChannelName: "缓存命中",
				Model: actualModel, StatusCode: 200, DurationMs: duration,
			})
			c.Data(200, "application/json", cached)
			return
		}
	}

	result, err := service.RouteRequest(actualModel, body, tokenKey)
	if err != nil {
		// 检查是否是快速失败（消息格式问题，如 tool_calls 不匹配）
		// 这种错误 fallback 到其他模型也没用，直接返回
		isFastFail := strings.Contains(err.Error(), "快速失败") ||
			strings.Contains(err.Error(), "消息格式错误")
		
		if isFastFail {
			duration := time.Since(startTime).Milliseconds()
			model.DB.Create(&model.RequestLog{
				TokenName: apiToken.Name, ChannelName: "",
				Model: req.Model, StatusCode: 400, DurationMs: duration,
			})
			respondError(c, http.StatusBadRequest, ErrInvalidRequest, err.Error())
			return
		}

		// 智能路由降级失败时，先试同级备选模型
		alternatives := service.GetAlternativeModels(actualModel)
		for _, altModel := range alternatives {
			var altReqMap map[string]interface{}
			if err := json.Unmarshal(body, &altReqMap); err == nil {
				altReqMap["model"] = altModel
				altBody, _ := json.Marshal(altReqMap)
				result, err = service.RouteRequest(altModel, altBody, tokenKey)
				if err == nil {
					log.Printf("[路由] 降级 %s 失败，备选 %s 成功", actualModel, altModel)
					break
				}
			}
		}
		
		// 备选也失败时，尝试用原始模型重试
		if err != nil {
			retryModel := req.Model
			if actualModel != req.Model {
				log.Printf("[路由] 备选均失败，尝试原始模型 %s", req.Model)
			} else {
				log.Printf("[路由] 原始模型 %s 失败，重试一次", req.Model)
			}
			
			var retryReqMap map[string]interface{}
			if err := json.Unmarshal(body, &retryReqMap); err == nil {
				retryReqMap["model"] = retryModel
				retryBody, _ := json.Marshal(retryReqMap)
				result, err = service.RouteRequest(retryModel, retryBody, tokenKey)
			}
		}
		
		if err != nil {
			duration := time.Since(startTime).Milliseconds()
			model.DB.Create(&model.RequestLog{
				TokenName: apiToken.Name, ChannelName: "无可用渠道",
				Model: req.Model, StatusCode: 502, DurationMs: duration,
			})
			respondError(c, http.StatusBadGateway, ErrChannelUnavail, err.Error())
			return
		}
	}
	defer result.Response.Body.Close()
	respBody, _ := io.ReadAll(result.Response.Body)
	duration := time.Since(startTime).Milliseconds()
	model.DB.Create(&model.RequestLog{
		TokenName: apiToken.Name, ChannelName: result.ChannelName,
		Model: req.Model, StatusCode: result.Response.StatusCode, DurationMs: duration,
	})

	// 设置响应头：返回实际路由的模型名
	c.Header("X-Actual-Model", actualModel)
	c.Header("X-Requested-Model", req.Model)

	// 解析 usage 字段并记录用量日志
	if result.Response.StatusCode == 200 {
		var upstreamResp struct {
			Usage struct {
				PromptTokens     int64 `json:"prompt_tokens"`
				CompletionTokens int64 `json:"completion_tokens"`
				TotalTokens      int64 `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(respBody, &upstreamResp); err == nil && upstreamResp.Usage.TotalTokens > 0 {
			// 查渠道获取实际模型名
			var channel model.Channel
			actualModel := req.Model
			if result.ChannelName != "" {
				model.DB.Where("name = ?", result.ChannelName).First(&channel)
				// 如果有 model_mapping，记录实际映射后的模型
				if channel.ModelMapping != "" {
					var mapping map[string]string
					if err := json.Unmarshal([]byte(channel.ModelMapping), &mapping); err == nil {
						if mapped, ok := mapping[req.Model]; ok {
							actualModel = mapped
						}
					}
				}
			}

			// 获取套餐名
			planName := ""
			if apiToken.RateLimitGroup != "" {
				planName = apiToken.RateLimitGroup
			}

			usageLog := model.UsageLog{
				TokenID:      apiToken.ID,
				TokenName:      apiToken.Name,
				PlanName:       planName,
				ChannelID:      channel.ID,
				ChannelName:    result.ChannelName,
				Model:          actualModel,
				InputTokens:    upstreamResp.Usage.PromptTokens,
				OutputTokens:   upstreamResp.Usage.CompletionTokens,
				TotalTokens:    upstreamResp.Usage.TotalTokens,
				StatusCode:     result.Response.StatusCode,
				DurationMs:     duration,
			}
			model.DB.Create(&usageLog)
		}
	}

	// 写入缓存（非流式且成功）
	if !isStream && result.Response.StatusCode == 200 && service.GlobalCache != nil && cacheKey != "" {
		service.GlobalCache.Set(cacheKey, respBody)
	}

	c.Data(result.Response.StatusCode, result.Response.Header.Get("Content-Type"), respBody)
}

// ===== 请求日志 =====

func getLogs(c *gin.Context) {
	var logs []model.RequestLog
	model.DB.Order("id DESC").Limit(100).Find(&logs)
	c.JSON(http.StatusOK, gin.H{"data": logs})
}

// ===== 成本分析（基于 UsageLog） =====

// getCostSummary 成本总览
func getCostSummary(c *gin.Context) {
	type CostRow struct {
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		Model        string  `json:"model"`
		Count        int64   `json:"count"`
	}

	var rows []CostRow
	model.DB.Raw(`SELECT model, sum(input_tokens) as input_tokens,
		sum(output_tokens) as output_tokens, count(*) as count
		FROM usage_logs GROUP BY model ORDER BY count DESC`).Scan(&rows)

	type SummaryItem struct {
		Model        string  `json:"model"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		TotalTokens  int64   `json:"total_tokens"`
		Count        int64   `json:"count"`
		CostYuan     float64 `json:"cost_yuan"`
	}

	var summary []SummaryItem
	var totalCost float64
	var totalTokens int64

	for _, r := range rows {
		cost := model.CalculateCost(r.InputTokens, r.OutputTokens, r.Model)
		totalCost += cost
		totalTokens += r.InputTokens + r.OutputTokens
		summary = append(summary, SummaryItem{
			Model:        r.Model,
			InputTokens:  r.InputTokens,
			OutputTokens: r.OutputTokens,
			TotalTokens:  r.InputTokens + r.OutputTokens,
			Count:        r.Count,
			CostYuan:     cost,
		})
	}

	// 今日、本周、本月统计
	var todayCost, weekCost, monthCost float64
	var todayTokens, weekTokens, monthTokens int64

	model.DB.Raw(`SELECT coalesce(sum(input_tokens+output_tokens),0) FROM usage_logs
		WHERE date(created_at)=date('now','localtime')`).Scan(&todayTokens)
	model.DB.Raw(`SELECT coalesce(sum(input_tokens+output_tokens),0) FROM usage_logs
		WHERE created_at >= datetime('now', '-7 days', 'localtime')`).Scan(&weekTokens)
	model.DB.Raw(`SELECT coalesce(sum(input_tokens+output_tokens),0) FROM usage_logs
		WHERE created_at >= datetime('now', '-30 days', 'localtime')`).Scan(&monthTokens)

	// 用默认模型估算成本（可以用 qwen3.7-plus 作为参考成本）
	// 更准确：重新计算
	type AllRow struct {
		Input  int64
		Output int64
		Model  string
	}
	var todayRows []AllRow
	model.DB.Raw(`SELECT input_tokens as input, output_tokens as output, model FROM usage_logs
		WHERE date(created_at)=date('now','localtime')`).Scan(&todayRows)
	for _, r := range todayRows {
		todayCost += model.CalculateCost(r.Input, r.Output, r.Model)
	}
	var weekRows []AllRow
	model.DB.Raw(`SELECT input_tokens as input, output_tokens as output, model FROM usage_logs
		WHERE created_at >= datetime('now', '-7 days', 'localtime')`).Scan(&weekRows)
	for _, r := range weekRows {
		weekCost += model.CalculateCost(r.Input, r.Output, r.Model)
	}
	var monthRows []AllRow
	model.DB.Raw(`SELECT input_tokens as input, output_tokens as output, model FROM usage_logs
		WHERE created_at >= datetime('now', '-30 days', 'localtime')`).Scan(&monthRows)
	for _, r := range monthRows {
		monthCost += model.CalculateCost(r.Input, r.Output, r.Model)
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"total_cost":    totalCost,
		"total_tokens":  totalTokens,
		"today_cost":    todayCost,
		"today_tokens":  todayTokens,
		"week_cost":     weekCost,
		"week_tokens":   weekTokens,
		"month_cost":    monthCost,
		"month_tokens":  monthTokens,
		"by_model":      summary,
	}})
}

// getCostByPlan 按套餐维度统计
func getCostByPlan(c *gin.Context) {
	type PlanRow struct {
		PlanName     string  `json:"plan_name"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		Count        int64   `json:"count"`
		CostYuan     float64 `json:"cost_yuan"`
	}

	var rows []struct {
		PlanName     string `gorm:"column:plan_name"`
		InputTokens  int64
		OutputTokens int64
		Count        int64
	}
	model.DB.Raw(`SELECT plan_name, sum(input_tokens) as input_tokens,
		sum(output_tokens) as output_tokens, count(*) as count
		FROM usage_logs GROUP BY plan_name ORDER BY count DESC`).Scan(&rows)

	var result []PlanRow
	for _, r := range rows {
		// 用默认模型价格估算
		cost := model.CalculateCost(r.InputTokens, r.OutputTokens, "default")
		result = append(result, PlanRow{
			PlanName:     r.PlanName,
			InputTokens:  r.InputTokens,
			OutputTokens: r.OutputTokens,
			Count:        r.Count,
			CostYuan:     cost,
		})
	}

	c.JSON(http.StatusOK, gin.H{"data": result})
}

// getCostTrend 近 7 天成本趋势
func getCostTrend(c *gin.Context) {
	type DayRow struct {
		Date         string  `json:"date"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		Count        int64   `json:"count"`
	}

	var rows []DayRow
	model.DB.Raw(`SELECT date(created_at) as date,
		sum(input_tokens) as input_tokens,
		sum(output_tokens) as output_tokens,
		count(*) as count
		FROM usage_logs
		WHERE created_at >= datetime('now', '-7 days', 'localtime')
		GROUP BY date(created_at)
		ORDER BY date ASC`).Scan(&rows)

	type TrendItem struct {
		Date        string  `json:"date"`
		TotalTokens int64   `json:"total_tokens"`
		Count       int64   `json:"count"`
		CostYuan    float64 `json:"cost_yuan"`
	}

	var trend []TrendItem
	for _, r := range rows {
		cost := model.CalculateCost(r.InputTokens, r.OutputTokens, "default")
		trend = append(trend, TrendItem{
			Date:        r.Date,
			TotalTokens: r.InputTokens + r.OutputTokens,
			Count:       r.Count,
			CostYuan:    cost,
		})
	}

	c.JSON(http.StatusOK, gin.H{"data": trend})
}

// ===== 模型列表 =====

func listModels(c *gin.Context) {
	var channels []model.Channel
	model.DB.Where("status = ?", 1).Find(&channels)
	models := make(map[string]bool)
	for _, ch := range channels {
		for _, m := range parseModels(ch.Models) {
			models[m] = true
		}
	}
	modelList := make([]string, 0, len(models))
	for m := range models {
		modelList = append(modelList, m)
	}
	c.JSON(http.StatusOK, gin.H{"data": modelList})
}

// ===== 用量统计 =====

func getUsageStats(c *gin.Context) {
	type DailyStat struct {
		Date   string `json:"date"`
		Count  int64  `json:"count"`
		AvgMs  int64  `json:"avg_ms"`
		Errors int64  `json:"errors"`
	}
	var dailyStats []DailyStat
	model.DB.Raw(`SELECT date(created_at) as date, count(*) as count,
		CAST(AVG(duration_ms) AS INTEGER) as avg_ms,
		SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END) as errors
		FROM request_logs WHERE created_at > datetime('now', '-7 days')
		GROUP BY date(created_at) ORDER BY date DESC`).Scan(&dailyStats)

	var totalCount, totalErrors int64
	var avgDuration int64
	model.DB.Model(&model.RequestLog{}).Count(&totalCount)
	model.DB.Model(&model.RequestLog{}).Where("status_code >= 400").Count(&totalErrors)
	model.DB.Raw("SELECT CAST(AVG(duration_ms) AS INTEGER) FROM request_logs").Scan(&avgDuration)

	type TokenStat struct {
		TokenName string `json:"token_name"`
		Count     int64  `json:"count"`
	}
	var tokenStats, channelStats []TokenStat
	model.DB.Raw("SELECT token_name, count(*) as count FROM request_logs GROUP BY token_name ORDER BY count DESC LIMIT 10").Scan(&tokenStats)
	model.DB.Raw("SELECT channel_name as token_name, count(*) as count FROM request_logs GROUP BY channel_name ORDER BY count DESC LIMIT 10").Scan(&channelStats)

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"total_requests": totalCount, "total_errors": totalErrors,
		"avg_duration_ms": avgDuration, "daily": dailyStats,
		"by_token": tokenStats, "by_channel": channelStats,
	}})
}

// ===== Token 查询（客户用）=====

func tokenInfo(c *gin.Context) {
	tokenKey := c.Query("token")
	if tokenKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供 token"})
		return
	}

	var token model.Token
	if err := model.DB.Where("key = ?", tokenKey).First(&token).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "token 不存在"})
		return
	}

	// 计算使用情况
	now := time.Now().Unix()
	fiveHoursAgo := now - 5*3600
	sevenDaysAgo := now - 7*24*3600

	var count5h, count7d int64
	model.DB.Model(&model.RateLimit{}).Where("token_id = ? AND request_time > ?", token.ID, fiveHoursAgo).Count(&count5h)
	model.DB.Model(&model.RateLimit{}).Where("token_id = ? AND request_time > ?", token.ID, sevenDaysAgo).Count(&count7d)

	// 从 usage_logs 查累计调用次数 + 累计 tokens
	type TokenUsage struct {
		Calls int64 `gorm:"column:calls"`
		Toks  int64 `gorm:"column:toks"`
	}
	var total TokenUsage
	model.DB.Raw(`SELECT count(*) as calls, coalesce(sum(total_tokens),0) as toks FROM usage_logs WHERE token_id = ?`, token.ID).Scan(&total)

	// 本周累计
	var week TokenUsage
	model.DB.Raw(`SELECT count(*) as calls, coalesce(sum(total_tokens),0) as toks FROM usage_logs WHERE token_id = ? AND created_at >= datetime('now', '-7 days', 'localtime')`, token.ID).Scan(&week)

	// 套餐信息
	var planDisplayName, planDesc string
	var limit5h, weeklyMax int64
	var skipHourly bool
	if token.RateLimitGroup != "" {
		var plan model.Plan
		if err := model.DB.Where("name = ?", token.RateLimitGroup).First(&plan).Error; err == nil {
			limit5h = plan.Hourly5Max
			weeklyMax = plan.WeeklyMax
			skipHourly = plan.SkipHourly
			planDisplayName = plan.DisplayName
			planDesc = plan.Description
		}
	}

	// 状态判断
	status := "正常"
	if token.Status == 2 {
		status = "已禁用"
	} else if token.ExpiredTime > 0 && now > token.ExpiredTime {
		status = "已过期"
	} else if token.ActivatedAt == 0 {
		status = "待激活"
	}

	// 计算剩余时间（向上取整）
	var remainingDays int
	var expireDate string
	if token.ExpiredTime > 0 {
		remainingSeconds := token.ExpiredTime - now
		remainingDays = int((remainingSeconds + 86399) / 86400) // 向上取整
		expireDate = time.Unix(token.ExpiredTime, 0).Format("2006-01-02 15:04:05")
	} else {
		remainingDays = -1
		expireDate = "永不过期"
	}

	c.JSON(http.StatusOK, gin.H{
		"status":           status,
		"token_name":       token.Name,
		"plan":             token.RateLimitGroup,
		"plan_name":        planDisplayName,
		"plan_desc":        planDesc,
		"skip_hourly":      skipHourly,
		"limit_5h":         limit5h,
		"used_5h":          count5h,
		"remaining_5h":     limit5h - count5h,
		"weekly_max":       weeklyMax,
		"used_7d":          count7d,
		"total_calls":      total.Calls,
		"total_tokens":     total.Toks,
		"week_calls":       week.Calls,
		"week_tokens":      week.Toks,
		"activated_at": func() string {
			if token.ActivatedAt == 0 {
				return "未激活"
			}
			return time.Unix(token.ActivatedAt, 0).Format("2006-01-02 15:04:05")
		}(),
		"expired_at":    expireDate,
		"remaining_days": remainingDays,
	})
}

// ===== 辅助函数 =====

func generateTokenKey() string {
	return fmt.Sprintf("atm-%d", time.Now().UnixNano())
}

func extractToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if len(auth) > 7 && auth[:7] == "Bearer " {
		return auth[7:]
	}
	return ""
}

func parseModels(modelsStr string) []string {
	return strings.Split(modelsStr, ",")
}

// 导出日志为 CSV
func exportLogs(c *gin.Context) {
	var logs []model.RequestLog
	model.DB.Order("id DESC").Limit(1000).Find(&logs)
	
	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", "attachment; filename=atmapi_logs.csv")
	
	c.Writer.WriteString("ID,Time,Token,Channel,Model,Status,Duration(ms)\n")
	for _, log := range logs {
		c.Writer.WriteString(fmt.Sprintf("%d,%s,%s,%s,%s,%d,%d\n",
			log.ID, log.CreatedAt.Format("2006-01-02 15:04:05"),
			log.TokenName, log.ChannelName, log.Model,
			log.StatusCode, log.DurationMs))
	}
}

// 系统设置
func getSystemSettings(c *gin.Context) {
	settings := gin.H{
		"version": "2.0.0",
		"database": "SQLite",
		"port": 3002,
		"jwt_auth": true,
		"cors": true,
		"channels_count": func() int64 { var c int64; model.DB.Model(&model.Channel{}).Count(&c); return c }(),
		"tokens_count": func() int64 { var c int64; model.DB.Model(&model.Token{}).Count(&c); return c }(),
		"users_count": func() int64 { var c int64; model.DB.Model(&model.User{}).Count(&c); return c }(),
		"logs_count": func() int64 { var c int64; model.DB.Model(&model.RequestLog{}).Count(&c); return c }(),
	}
	c.JSON(http.StatusOK, gin.H{"data": settings})
}

// ===== 支付相关（待对接）=====

// PlanOption 套餐选项
var planOptions = []struct {
	Name  string
	Price float64
	Desc  string
}{
	{"trial", 0, "体验版：5h 50次 / 周500次"},
	{"basic", 29.9, "基础版 ¥29.9：5h 100次 / 周5600次"},
	{"pro", 49.9, "专业版 ¥49.9：5h 300次 / 周16800次"},
	{"premium", 89, "旗舰版 ¥89：5h 500次 / 周28000次"},
}

// Order 订单模型
var orders = struct {
	sync.RWMutex
	m map[string]*OrderInfo
}{m: make(map[string]*OrderInfo)}

type OrderInfo struct {
	ID          string    `json:"id"`
	PlanName    string    `json:"plan_name"`
	Price       float64   `json:"price"`
	Status      string    `json:"status"`   // pending / paid / cancelled / expired
	PayURL      string    `json:"pay_url"`  // 支付宝/微信支付链接
	UserOpenID  string    `json:"user_open_id"`
	CreatedAt   time.Time `json:"created_at"`
	PaidAt      time.Time `json:"paid_at"`
	TokenName   string    `json:"token_name"` // 支付成功后自动创建的 Token 名
}

// hmacSign 简单签名验证（后续替换为支付宝官方 SDK）
func hmacSign(data, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}

// createOrder 创建订单
// POST /api/v1/payment/create-order
// Body: {"plan":"basic", "user_open_id":"ou_xxx"}
// Resp: {"order_id":"xxx", "pay_url":"https://..."}
func createOrder(c *gin.Context) {
	var req struct {
		PlanName   string `json:"plan" binding:"required"`
		UserOpenID string `json:"user_open_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供套餐名"})
		return
	}

	// 验证套餐是否存在
	var found *struct{ Name string; Price float64; Desc string }
	for _, p := range planOptions {
		if p.Name == req.PlanName {
			found = &struct{ Name string; Price float64; Desc string }{p.Name, p.Price, p.Desc}
			break
		}
	}
	if found == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("套餐 %s 不存在", req.PlanName)})
		return
	}

	// 生成订单
	orderID := fmt.Sprintf("ORD%d", time.Now().UnixNano())
	order := &OrderInfo{
		ID:         orderID,
		PlanName:   req.PlanName,
		Price:      found.Price,
		Status:     "pending",
		UserOpenID: req.UserOpenID,
		CreatedAt:  time.Now(),
	}

	// 如果套餐不是免费的，生成支付链接
	// 建国在此处替换为真实的支付宝/微信统一收单链接
	if order.Price > 0 {
		// TODO: 替换为真实的支付宝当面付/小程序支付链接
		// lookup() 从阿里申请的支付链接，拼上订单参数
		// 例如："https://openapi.alipay.com/gateway.do?method=alipay.trade.precreate&..."
		order.PayURL = fmt.Sprintf("https://pay.aitomoney.online/pay?order_id=%s&plan=%s&price=%.2f", orderID, req.PlanName, found.Price)
	} else {
		// 免费套餐直接激活
		order.Status = "paid"
		order.PaidAt = time.Now()
		activatePlan(c, req.UserOpenID, req.PlanName, orderID)
	}

	orders.Lock()
	orders.m[orderID] = order
	orders.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"order_id": orderID,
		"pay_url":  order.PayURL,
		"price":    order.Price,
		"plan":     req.PlanName,
		"status":   order.Status,
	})
}

// activatePlan 支付成功后激活套餐：创建 Token + 绑定限流组
func activatePlan(c *gin.Context, userOpenID, planName, orderID string) {
	// 验证套餐存在
	if _, err := service.GetPlan(planName); err != nil {
		log.Printf("[支付] 套餐 %s 不存在，无法激活", planName)
		return
	}

	// 查找或创建用户
	var user model.User
	result := model.DB.Where("username = ?", userOpenID).First(&user)
	if result.Error != nil {
		user = model.User{
			Username: userOpenID,
			Password: orderID, // 首次使用需改密码
			Role:     1,
			Status:   1,
			DisplayName: planName + "用户",
			Quota:    1000000,
		}
		model.DB.Create(&user)
	}

	// 创建 Token
	tokenName := fmt.Sprintf("%s-%s", planName, orderID[len(orderID)-6:])
	token := model.Token{
		UserID:         user.ID,
		Name:           tokenName,
		Key:            fmt.Sprintf("atm-%s-%d", orderID[:8], time.Now().UnixNano()),
		Status:         1,
		RemainQuota:    -1,           // 无限配额（靠滑动窗口限速）
		UnlimitedQuota: true,
		RateLimitGroup: planName,
		CreatedTime:    time.Now().Unix(),
		ActivatedAt:    time.Now().Unix(),
		ExpiredTime:    time.Now().AddDate(0, 1, 0).Unix(), // 1个月
	}
	model.DB.Create(&token)

	// 将 token 回写到订单
	orders.Lock()
	if o, ok := orders.m[orderID]; ok {
		o.TokenName = tokenName
		o.Status = "paid"
	}
	orders.Unlock()

	log.Printf("[支付] 订单 %s 激活成功: plan=%s, token=%s, user=%d", orderID, planName, tokenName, user.ID)
}

// alipayNotify 支付宝异步回调
// POST /api/v1/payment/alipay-notify
// 阿里支付成功后，会 POST 以下字段过来：
// out_trade_no（订单号）, trade_status（TRADE_SUCCESS）, total_amount, sign 等
func alipayNotify(c *gin.Context) {
	// 需要验证签名
	c.Request.ParseForm()
	params := c.Request.Form

	log.Printf("[支付] 收到支付宝回调: %v", params)

	orderID := params.Get("out_trade_no")
	tradeStatus := params.Get("trade_status")

	// 验证签名 TODO: 替换为支付宝官方 SDK 验证
	sign := params.Get("sign")
	_ = sign

	if orderID == "" || tradeStatus == "" {
		c.String(http.StatusBadRequest, "fail")
		return
	}

	if tradeStatus == "TRADE_SUCCESS" || tradeStatus == "TRADE_FINISHED" {
		orders.RLock()
		order, exists := orders.m[orderID]
		orders.RUnlock()

		if !exists {
			log.Printf("[支付] 订单 %s 不存在", orderID)
			c.String(http.StatusOK, "fail")
			return
		}

		if order.Status == "paid" {
			c.String(http.StatusOK, "success")
			return
		}

		// 激活套餐
		activatePlan(c, order.UserOpenID, order.PlanName, orderID)
	}

	// 支付宝要求返回 "success"
	c.String(http.StatusOK, "success")
}

// wechatNotify 微信异步回调（接口与支付宝类似）
// POST /api/v1/payment/wechat-notify
func wechatNotify(c *gin.Context) {
	body, _ := io.ReadAll(c.Request.Body)
	log.Printf("[支付] 收到微信回调: %s", string(body))

	// TODO: 解析微信支付结果通知 XML
	// 验证签名 -> 提取 orderID -> activatePlan()

	c.String(http.StatusOK, `<xml><return_code><![CDATA[SUCCESS]]></return_code></xml>`)
}

// getOrderStatus 查询订单状态
// GET /api/v1/payment/order-status?order_id=xxx
func getOrderStatus(c *gin.Context) {
	orderID := c.Query("order_id")
	if orderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供 order_id"})
		return
	}

	orders.RLock()
	order, exists := orders.m[orderID]
	orders.RUnlock()

	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "订单不存在"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"order_id":    order.ID,
		"plan":        order.PlanName,
		"price":       order.Price,
		"status":      order.Status,
		"created_at":  order.CreatedAt,
		"paid_at":     order.PaidAt,
		"token_name":  order.TokenName,
	})
}

// getPayments 管理后台：查看所有支付记录
func getPayments(c *gin.Context) {
	orders.RLock()
	list := make([]*OrderInfo, 0, len(orders.m))
	for _, o := range orders.m {
		list = append(list, o)
	}
	orders.RUnlock()

	// 按时间倒序
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if list[j].CreatedAt.After(list[i].CreatedAt) {
				list[i], list[j] = list[j], list[i]
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"data": list})
}

// refundPayment 管理后台：退款
func refundPayment(c *gin.Context) {
	var req struct {
		OrderID string `json:"order_id" binding:"required"`
		Reason  string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供订单ID"})
		return
	}

	orders.RLock()
	order, exists := orders.m[req.OrderID]
	orders.RUnlock()

	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "订单不存在"})
		return
	}

	if order.Status != "paid" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "只有已支付的订单才能退款"})
		return
	}

	// TODO: 调用支付宝/微信退款 API
	// alipay.trade.refund(...)

	order.Status = "refunded"
	log.Printf("[支付] 订单 %s 已退款: %s", req.OrderID, req.Reason)
	c.JSON(http.StatusOK, gin.H{"message": "退款成功", "order_id": req.OrderID})
}
