package api
import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"atmapi/internal/middleware"
	"atmapi/internal/model"
	"atmapi/internal/service"
	"github.com/gin-gonic/gin"
)
var dbgFile *os.File
func initDbgLog() {
	dbgFile, _ = os.OpenFile("/tmp/atmapi-img-debug.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
}
func dbgLog(format string, args ...interface{}) {
	msg := fmt.Sprintf(time.Now().Format("2006/01/02 15:04:05 ")+format+"\n", args...)
	if dbgFile != nil {
		fmt.Fprintf(dbgFile, msg)
		dbgFile.Sync()
	}
	log.Printf(format, args...) // 也输出到 stderr
}
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
	initDbgLog()
	// 安全 Headers 中间件
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
		c.Writer.Header().Set("X-Frame-Options", "SAMEORIGIN")
		c.Writer.Header().Set("X-XSS-Protection", "1; mode=block")
		c.Writer.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})
	r.Use(func(c *gin.Context) {
		// 静态文件不缓存（开发阶段）
		if strings.HasPrefix(c.Request.URL.Path, "/static/") {
			c.Writer.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			c.Writer.Header().Set("Pragma", "no-cache")
			c.Writer.Header().Set("Expires", "0")
		}
		c.Next()
	})
	r.Static("/static", "./web/static")
	r.GET("/", indexPage)
	r.GET("/pay", func(c *gin.Context) { c.Redirect(301, "/static/pay.html?"+c.Request.URL.RawQuery) })
	r.GET("/account", func(c *gin.Context) { c.Redirect(301, "/static/pay.html?"+c.Request.URL.RawQuery) })
	r.GET("/health", healthCheck)
	r.GET("/cache/stats", cacheStats)
	r.GET("/token-info", func(c *gin.Context) { c.File("./web/static/token-info.html") })
	// 短链接跳转：/go/<orderNo> → 302 到支付宝长链接
	r.GET("/go/:orderNo", func(c *gin.Context) {
		orderNo := c.Param("orderNo")
		var order model.Order
		if err := model.DB.Where("order_no = ?", orderNo).First(&order).Error; err != nil {
			c.String(http.StatusNotFound, "订单不存在")
			return
		}
		if order.PayURL == "" {
			c.String(http.StatusNotFound, "支付链接未生成")
			return
		}
		c.Redirect(http.StatusFound, order.PayURL)
	})
	r.GET("/dashboard", func(c *gin.Context) { c.Redirect(301, "/static/dashboard.html") })
	r.GET("/cost-dashboard", func(c *gin.Context) { c.Redirect(301, "/static/cost-dashboard.html") })
	r.GET("/monitor", func(c *gin.Context) { c.Redirect(301, "/static/monitor.html") })
	// MCP 端点（Model Context Protocol）
	r.GET("/mcp", mcpHandle)
	r.POST("/mcp", mcpHandle)
	// Hermes 兼容路由（Hermes 不拼 /v1 前缀）
	r.POST("/chat/completions", chatCompletions)
	// OpenAI 兼容路由（/v1）— 给 OpenClaw Gateway 和标准客户端用
	oai := r.Group("/v1")
	{
		oai.POST("/chat/completions", chatCompletions)
		oai.GET("/models", listModels)
		oai.GET("/usage", getTokenUsage)
	}
	v1 := r.Group("/api/v1")
	{
		v1.POST("/login", login)
		v1.POST("/chat/completions", chatCompletions)
		v1.POST("/register", register)
		v1.GET("/models", listModels)
		v1.GET("/token-info", tokenInfo) // 客户查询 token 信息
		v1.GET("/stats", publicStats) // 公开统计（监控中心用）
		managed := v1.Group("")
		managed.Use(middleware.AuthRequired())
		{
			managed.GET("/tokens", getTokens)
			managed.POST("/tokens", createToken)
			managed.POST("/tokens/batch", batchCreateTokens)
			managed.PUT("/tokens/:id", updateToken)
			managed.DELETE("/tokens/:id", deleteToken)
			managed.GET("/channels", getChannels)
			managed.POST("/channels", createChannel)
			managed.PUT("/channels/:id", updateChannel)
			managed.DELETE("/channels/:id", deleteChannel)
			managed.POST("/channels/:id/test", testChannel)
			// 定价管理 API
			managed.GET("/pricing", getPricings)
			managed.GET("/pricing/:id", getPricingByID)
			managed.POST("/pricing", createPricing)
			managed.PUT("/pricing/:id", updatePricing)
			managed.DELETE("/pricing/:id", deletePricing)
			managed.GET("/logs", getLogs)
			managed.GET("/usage", getUsageStats)
			managed.GET("/settings", getSystemSettings)
			managed.GET("/logs/export", exportLogs)
			// 成本分析 API
			managed.GET("/cost-summary", getCostSummary)
			managed.GET("/cost-by-plan", getCostByPlan)
			managed.GET("/cost-trend", getCostTrend)
			// Phase 2C 成本仪表盘 API
			managed.GET("/dashboard", getDashboard)
			managed.GET("/token/:id/cost", getTokenCost)
			managed.GET("/cost-dashboard/enhanced", getDashboardEnhanced)
			managed.GET("/dashboard/v2", getDashboardV2)
			managed.GET("/token-ranking", getTokenRanking)
			managed.GET("/alerts", getAlerts)
			// 套餐管理
			managed.GET("/plans", getPlans)
			managed.POST("/plans/sync", syncPlans)
			// API Key 生成（绑定套餐）
			managed.POST("/keys/generate", generateKey)
			managed.POST("/keys/:id/bind-plan", bindPlanToKey)
			managed.GET("/keys/:id/plan", getKeyPlanInfo)
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
			// 开发测试端点（上线前删除）
			payment.POST("/test-activate", testActivateOrder)
			// 4. 查询订单状态（用户查看支付是否成功）
			payment.GET("/order-status", getOrderStatus)
		}
	}
}
func indexPage(c *gin.Context)     { c.File("./web/static/index.html") }
func healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "version": "2.1.0", "time": time.Now().Format("2006-01-02 15:04:05")})
}
func cacheStats(c *gin.Context) {
	stats := service.GetCacheStats()
	c.JSON(http.StatusOK, gin.H{"data": stats})
}
// ===== 登录认证 =====
func login(c *gin.Context) {
	// 防暴力登录：检查 IP 限流
	ip := c.ClientIP()
	if !checkLoginRateLimit(ip) {
		respondError(c, http.StatusTooManyRequests, ErrRateLimitExceeded, "登录尝试过于频繁，请稍后再试")
		return
	}

	// 从本地文件读取登录密钥
	var req struct {
		Key string `json:"key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest, "请提供登录密钥")
		return
	}

	// 读取本地密钥文件
	localKeyBytes, err := os.ReadFile("/home/admin/.openclaw/atmApi-adminkey.secret")
	if err != nil {
		log.Printf("[登录] 密钥文件读取失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器配置异常"})
		return
	}
	localKey := strings.TrimSpace(string(localKeyBytes))

	if req.Key != localKey {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "密钥错误"})
		return
	}

	// 生成 JWT token
	token, err := middleware.GenerateToken(1, "admin", 100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成 token 失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "登录成功", "token": token,
		"user_id": 1, "username": "admin",
		"display_name": "系统管理员", "role": 100,
	})
}
func register(c *gin.Context) {
	// 防暴力注册：检查 IP 限流
	ip := c.ClientIP()
	if !checkRegisterRateLimit(ip) {
		respondError(c, http.StatusTooManyRequests, ErrRateLimitExceeded, "注册过于频繁，请稍后再试")
		return
	}
	var req struct {
		Username    string `json:"username" binding:"required"`
		Password    string `json:"password" binding:"required,min=6"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest, err.Error())
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
	q := model.DB
	// 支持按套餐线筛选：?plan_group=dp-a4
	if pg := c.Query("plan_group"); pg != "" {
		q = q.Where("plan_group = ?", pg)
	}
	// 支持按套餐名筛选：?plan_name=basic
	if pn := c.Query("plan_name"); pn != "" {
		q = q.Where("plan_name = ?", pn)
	}
	q.Find(&tokens)
	c.JSON(http.StatusOK, gin.H{"data": tokens})
}
func createToken(c *gin.Context) {
	var req struct {
		UserID           uint   `json:"user_id" binding:"required"`
		Name             string `json:"name" binding:"required"`
		RemainQuota      int64  `json:"remain_quota"`
		UnlimitedQuota   bool   `json:"unlimited_quota"`
		ExpiredTime      int64  `json:"expired_time"`
		RateLimitGroup   string `json:"rate_limit_group"`
		PlanGroup        string `json:"plan_group"`        // 套餐线：dp-a4/glm-5.2
		PlanName         string `json:"plan_name"`         // 套餐名：basic/pro/openclaw-pro
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 如果指定了 plan_name，从 plans 表加载配置
	if req.PlanName != "" {
		var plan model.Plan
		if err := model.DB.Where("name = ?", req.PlanName).First(&plan).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("套餐 %s 不存在", req.PlanName)})
			return
		}
		req.UnlimitedQuota = true
		req.RemainQuota = -1
		// plan_name 也作为 rate_limit_group（限流逻辑复用）
		if req.RateLimitGroup == "" {
			req.RateLimitGroup = req.PlanName
		}
	}
	token := model.Token{
		UserID: req.UserID, Name: req.Name, Key: generateTokenKey(),
		Status: 1, RemainQuota: req.RemainQuota,
		UnlimitedQuota: req.UnlimitedQuota, ExpiredTime: req.ExpiredTime,
		RateLimitGroup: req.RateLimitGroup,
		PlanGroup:      req.PlanGroup,
		PlanName:       req.PlanName,
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
	apiToken, err := model.FindByKey(tokenKey)
	if err != nil {
		respondError(c, http.StatusUnauthorized, ErrTokenNotFound, "token 不存在或已禁用")
		return
	}
	
	// 验证 token 是否有效（在图片缓存逻辑之前）
	if apiToken.ID == 0 {
		respondError(c, http.StatusUnauthorized, ErrTokenNotFound, "token 不存在或已禁用")
		return
	}
	
	// 限流检查（5h/日/周/月/图片次数）
	rlResult := service.CheckRateLimit(apiToken)
	if !rlResult.Allowed {
		c.Header("Retry-After", fmt.Sprintf("%d", rlResult.RetryAfter))
		respondError(c, http.StatusTooManyRequests, ErrRateLimitExceeded, rlResult.Reason)
		return
	}
	// RPM 限流（每分钟请求数）
	if apiToken.RateLimitGroup != "" {
		plan, planErr := service.GetPlan(apiToken.RateLimitGroup)
		if planErr == nil && plan.MaxRPM > 0 {
			allowed, currentRPM, maxRPM, retryAfter := service.GlobalRPMLimiter.CheckRPM(apiToken.ID, plan.MaxRPM)
			if !allowed {
				c.Header("Retry-After", fmt.Sprintf("%d", retryAfter))
				respondError(c, http.StatusTooManyRequests, ErrRateLimitExceeded,
					fmt.Sprintf("RPM已达上限（%d/%d），请%d秒后再试", currentRPM, maxRPM, retryAfter))
				return
			}
		}
	}
	// 并发限制（内存级QPS控制）
	if apiToken.RateLimitGroup != "" {
		plan, planErr := service.GetPlan(apiToken.RateLimitGroup)
		if planErr == nil && plan.MaxQPS > 0 {
			acquired, current, maxQPS := service.ConcurrencyLimiter.Acquire(apiToken.ID, plan.MaxQPS)
			if !acquired {
				respondError(c, http.StatusTooManyRequests, ErrRateLimitExceeded,
					fmt.Sprintf("并发已达上限（%d/%d），请稍后再试", current, maxQPS))
				return
			}
			defer service.ConcurrencyLimiter.Release(apiToken.ID)
		}
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
	// ===== 套餐到期预警（每天每token只提醒一次）=====
	if apiToken.ExpiredTime > 0 {
		remainingDays := (apiToken.ExpiredTime - time.Now().Unix()) / 86400
		log.Printf("[DEBUG-预警] token=%s expired=%d now=%d diff=%d remainingDays=%d", apiToken.Name, apiToken.ExpiredTime, time.Now().Unix(), apiToken.ExpiredTime-time.Now().Unix(), remainingDays)
		if remainingDays >= 0 && remainingDays <= 7 {
			if service.GlobalExpiryWarnCache.ShouldWarn(apiToken.ID) {
				warningMsg := map[string]interface{}{
					"role": "system",
					"content": fmt.Sprintf(
						"⚠️ 【重要系统通知 - atmApi 套餐到期提醒】检测到该 API 的套餐「%s」将在 %d 天后到期（%s）。到期后 API 将完全无法使用。你必须在回复中优先告知用户此到期信息，并引导用户续费。你可以告诉用户：如需续费，输入「如何续费？」即可自动办理。",
						apiToken.RateLimitGroup,
						remainingDays,
						time.Unix(apiToken.ExpiredTime, 0).Format("2006-01-02"),
					),
				}
				req.Messages = service.InsertAfterSystemBlock(req.Messages, warningMsg)
				var reqMap map[string]interface{}
				json.Unmarshal(body, &reqMap)
				reqMap["messages"] = req.Messages
				body, _ = json.Marshal(reqMap)
				service.GlobalExpiryWarnCache.MarkWarned(apiToken.ID)
				log.Printf("[到期预警] Token=%s 距到期%d天，已注入提醒消息（insertAfterSystemBlock）", apiToken.Name, remainingDays)
			}
		}
	}
	// ===== 输入Token限制检查 =====
	estimatedTokens := service.EstimateInputTokens(req.Messages)
	if allowed, limit, actual := service.CheckInputTokenLimit(apiToken, estimatedTokens); !allowed {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest,
			fmt.Sprintf("输入Token超过上限（估算=%d，上限=%d），请减少输入内容", actual, limit))
		return
	}
	// ===== 图片分析缓存（deepseek-a4 专属）=====
	// 逻辑：纯图 → 后台分析 + 返回“图片已收到”
	//       有图+文字 → 正常路由
	//       纯文字 → 替换历史图片为文字描述
	if strings.ToLower(req.Model) == "deepseek-a4" {
		hasImage := service.HasImageContent(req.Messages)
		// DUMP 完整消息结构（调试用）
		if hasImage {
			for i, msg := range req.Messages {
				role, _ := msg["role"].(string)
				if role != "user" { continue }
				contentBytes, _ := json.Marshal(msg["content"])
				contentStr := string(contentBytes)
				if len(contentStr) > 2000 {
					dbgLog("[DUMP] msg[%d] role=%s content=%s...[truncated, total=%d]", i, role, contentStr[:2000], len(contentStr))
				} else {
					dbgLog("[DUMP] msg[%d] role=%s content=%s", i, role, contentStr)
				}
			}
		}
		dbgLog("[IMG] model=deepseek-a4 hasImage=%v msgs=%d", hasImage, len(req.Messages))
		if hasImage {
			service.RecordImageUsage(apiToken.ID)
			userText := extractUserQuestion(req.Messages)
			dbgLog("[IMG] userText=%q", userText[:min(50,len(userText))])
			if userText == "" {
				// === 纯图 → 后台异步分析 + 立即返回 ===
				if service.GlobalImageAnalysis != nil {
					// v2: 直接转发原始 messages，不需要提取图片
					msgHash := service.HashMessages(req.Messages)
					dbgLog("[IMG] msgHash=%s", msgHash)
					service.GlobalImageAnalysis.AnalyzeAsync(msgHash, req.Messages)
				}
				// 记录日志
				duration := time.Since(startTime).Milliseconds()
				model.DB.Create(&model.RequestLog{
					TokenName: apiToken.Name, ChannelName: "图片分析",
					Model: req.Model, StatusCode: 200, DurationMs: duration,
				})
				service.RecordRequest(apiToken.ID)
				c.Header("X-Actual-Model", "deepseek-a4")
				c.Header("X-Requested-Model", req.Model)
				var reqMapChk map[string]interface{}
				json.Unmarshal(body, &reqMapChk)
				isStreamReq := false
				if v, ok := reqMapChk["stream"]; ok { isStreamReq, _ = v.(bool) }
				if isStreamReq {
					c.Header("Content-Type", "text/event-stream")
					c.Header("Cache-Control", "no-cache")
					c.Header("Connection", "keep-alive")
					c.Stream(func(w io.Writer) bool {
						chunk := fmt.Sprintf("data: {\"id\":\"imganalysis\",\"object\":\"chat.completion.chunk\",\"created\":%d,\"model\":\"deepseek-a4\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"图片已收到，你需要我帮你做什么？\"},\"finish_reason\":null}]}\n\n", time.Now().Unix())
						c.Writer.WriteString(chunk)
						c.Writer.WriteString("data: [DONE]\n\n")
						c.Writer.Flush()
						return false
					})
				} else {
					c.Data(200, "application/json", service.GenerateImageCacheResponse())
				}
				return
			}
			// 有图+有文字 → 继续往下正常路由
		} else if service.GlobalImageAnalysis != nil {
			// === 纯文字请求 → 替换历史图片为文字描述 ===
			newMsgs := service.ReplaceImagesWithText(req.Messages)
			dbgLog("[IMG] replace done, msgs=%d", len(newMsgs))
			if len(newMsgs) > 0 {
				req.Messages = newMsgs
				var reqMapNew map[string]interface{}
				json.Unmarshal(body, &reqMapNew)
				reqMapNew["messages"] = newMsgs
				body, _ = json.Marshal(reqMapNew)
			}
		}
	}
	// ===== 元数据清理：剥离 OpenClaw 注入的 Conversation info / Sender 元数据 =====
	// 2026-07-10：修复图片+文字路径跳过 analyzeComplexityV2 导致元数据泄漏到上游模型
	// 图片路径直接路由到 qwen3.7-plus，不走复杂度分析，因此之前的元数据剥离逻辑被跳过
	// 这里对所有 user 消息的 text content 统一做 stripMetadata
	{
		cleaned := false
		for i := range req.Messages {
			role, _ := req.Messages[i]["role"].(string)
			if role != "user" {
				continue
			}
			content := req.Messages[i]["content"]
			switch c := content.(type) {
			case string:
				if strings.Contains(c, "untrusted metadata") || strings.Contains(c, "Conversation info") {
					req.Messages[i]["content"] = stripMetadata(c)
					cleaned = true
				}
			case []interface{}:
				for _, part := range c {
					if pm, ok := part.(map[string]interface{}); ok {
						if typ, _ := pm["type"].(string); typ == "text" {
							if text, ok := pm["text"].(string); ok {
								if strings.Contains(text, "untrusted metadata") || strings.Contains(text, "Conversation info") {
									pm["text"] = stripMetadata(text)
									cleaned = true
								}
							}
						}
					}
				}
			}
		}
		if cleaned {
			var reqMapClean map[string]interface{}
			json.Unmarshal(body, &reqMapClean)
			cleanMsgs := make([]interface{}, len(req.Messages))
			for i, m := range req.Messages {
				cleanMsgs[i] = m
			}
			reqMapClean["messages"] = cleanMsgs
			body, _ = json.Marshal(reqMapClean)
			log.Printf("[元数据清理] 已剥离 OpenClaw 注入的 Conversation info / Sender 元数据")
		}
	}

	// ===== 输出控制：简洁回复约束（替代硬截断）=====
	// 2026-07-08：用 system message 约束输出长度，而不是 max_tokens 硬截断
	// 用户明确要求"详细/完整/长文"时不注入，尊重用户意图
	{
		longFormKeywords := []string{"详细", "完整", "长文", "展开", "深入", "全面", "长篇", "写一篇", "详细解释", "详细说明"}
		needLongForm := false
		// 检查最后一条用户消息
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if role, _ := req.Messages[i]["role"].(string); role == "user" {
				content, _ := req.Messages[i]["content"].(string)
				for _, kw := range longFormKeywords {
					if strings.Contains(content, kw) {
						needLongForm = true
					}
				}
				break
			}
		}
		if !needLongForm {
			// 注入简洁约束 system message（使用 InsertAfterSystemBlock 保证前缀稳定）
			conciseMsg := map[string]interface{}{
				"role":    "system",
				"content": "指令：请始终用最简洁的方式回复，避免任何多余解释和长篇背景介绍。回复总字数控制在300字以内，除非用户明确要求详细说明。",
			}
			// 使用 InsertAfterSystemBlock 而非 prepend，保证 messages[0] 稳定
			req.Messages = service.InsertAfterSystemBlock(req.Messages, conciseMsg)
			// 同步更新 body
			var reqMapNew map[string]interface{}
			json.Unmarshal(body, &reqMapNew)
			newMsgs := make([]interface{}, len(req.Messages))
			for i, m := range req.Messages {
				newMsgs[i] = m
			}
			reqMapNew["messages"] = newMsgs
			body, _ = json.Marshal(reqMapNew)
			log.Printf("[输出控制] token=%s 注入简洁回复约束（insertAfterSystemBlock）", apiToken.Name)
		} else {
			log.Printf("[输出控制] token=%s 用户要求长文，跳过简洁约束", apiToken.Name)
		}
	}
	// ===== 输出控制：max_tokens 安全上限（仅防极端情况）=====
	// 不再硬截断，只设一个很高的安全上限防止输出爆炸
	if apiToken.RateLimitGroup != "" {
		plan, _ := service.GetPlan(apiToken.RateLimitGroup)
		if plan != nil {
			// 安全上限 = 套餐上限 * 2，最低 16384
			safetyMax := plan.MaxOutputTokens * 2
			if safetyMax < 16384 {
				safetyMax = 16384
			}
			if safetyMax > 128000 {
				safetyMax = 128000
			}
			var reqMapCheck map[string]interface{}
			json.Unmarshal(body, &reqMapCheck)
			var userMaxTokens *int
			if v, ok := reqMapCheck["max_tokens"]; ok {
				if f, ok := v.(float64); ok {
					mt := int(f)
					userMaxTokens = &mt
				}
			}
			// 只在用户没设或超过安全上限时才注入
			if userMaxTokens == nil || *userMaxTokens > safetyMax {
				var reqMapNew map[string]interface{}
				json.Unmarshal(body, &reqMapNew)
				reqMapNew["max_tokens"] = safetyMax
				body, _ = json.Marshal(reqMapNew)
				log.Printf("[输出控制] token=%s plan=%s 安全上限max_tokens=%d",
					apiToken.Name, plan.Name, safetyMax)
			}
		}
	}
	// ===== 智能路由：根据请求复杂度选择模型 =====
	// 调试：打印最后3条消息角色
	lastRoles := ""
	start := len(req.Messages) - 3
	if start < 0 {
		start = 0
	}
	for i := start; i < len(req.Messages); i++ {
		r, _ := req.Messages[i]["role"].(string)
		if i == len(req.Messages)-1 {
			lastRoles += fmt.Sprintf("[%s]←最后", r)
		} else {
			lastRoles += fmt.Sprintf("[%s]→", r)
		}
	}
	log.Printf("[路由] 最后3条: %s", lastRoles)
	actualModel := service.SmartRoute(req.Model, req.Messages, tokenKey)

	// ===== 任务模式路由（Phase 2C） =====
	taskModeResult := service.ClassifyTaskMode(req.Messages)
	if taskModeResult.Model != "" && taskModeResult.Mode != service.TaskModeUnknown {
		// 任务模式覆盖路由结果（调试→pro，咨询→flash，闲聊→flash）
		if actualModel != taskModeResult.Model {
			log.Printf("[模式路由] 覆盖模型: %s → %s (mode=%s)", actualModel, taskModeResult.Model, taskModeResult.Mode)
			actualModel = taskModeResult.Model
		}
	}
	// 注入任务模式 hint
	if taskModeResult.Hint != "" {
		req.Messages = service.ApplyTaskModeHint(req.Messages, taskModeResult)
	}

	if actualModel != req.Model {
		// 替换请求体中的 model
		var reqMap map[string]interface{}
		json.Unmarshal(body, &reqMap)
		reqMap["model"] = actualModel
		body, _ = json.Marshal(reqMap)
	}

	// qwen3.7-plus max_tokens 上限为 65536，强制截断
	if actualModel == "qwen3.7-plus" {
		var reqMapCheck map[string]interface{}
		json.Unmarshal(body, &reqMapCheck)
		if mt, ok := reqMapCheck["max_tokens"].(float64); ok && mt > 65536 {
			reqMapCheck["max_tokens"] = 65536
			body, _ = json.Marshal(reqMapCheck)
			log.Printf("[输出控制] qwen3.7-plus max_tokens 截断至 65536 (原值: %.0f)", mt)
		}
	}

	// ===== 模型权限校验：token 的套餐必须允许此模型 =====
	apiPlanName := apiToken.PlanName
	if apiPlanName == "" {
		apiPlanName = apiToken.RateLimitGroup
	}
	if apiPlanName != "" {
		allowedModels := service.GetAllowedModels(apiPlanName)
		if len(allowedModels) > 0 {
			// 用户请求的模型名用于鉴权，deepseek-a4 是统一入口
			if req.Model == "deepseek-a4" && containsString(allowedModels, "deepseek-a4") {
				// deepseek-a4 在允许列表中 → 其下游模型自动放行
				goto modelAllowed
			}
			if !containsString(allowedModels, actualModel) {
				// actualModel 不在允许列表 → 尝试用 deepseek-a4 作为入口（套餐允许 a4）
				if containsString(allowedModels, "deepseek-a4") {
					goto modelAllowed
				}
				log.Printf("[鉴权] token=%s 套餐=%s 不允许模型=%s, allowed=%v",
					apiToken.Name, apiPlanName, actualModel, allowedModels)
				respondError(c, http.StatusForbidden, "MODEL_NOT_ALLOWED",
					fmt.Sprintf("当前套餐「%s」不支持模型「%s」，可用模型: %s",
						apiPlanName, actualModel, strings.Join(allowedModels, ", ")))
				return
			}
		}
	}
modelAllowed:
	// 检查缓存（只对非流式请求）
	var isStream bool
	var reqMap map[string]interface{}
	json.Unmarshal(body, &reqMap)
	if streamVal, ok := reqMap["stream"]; ok {
		isStream, _ = streamVal.(bool)
	}
	
	var cacheKey string
	if !isStream && service.GlobalCache != nil {
		// 从 reqMap 中提取 temperature 和 max_tokens
		temperature := 0.0
		maxTokens := 0
		if t, ok := reqMap["temperature"].(float64); ok {
			temperature = t
		}
		if m, ok := reqMap["max_tokens"].(float64); ok {
			maxTokens = int(m)
		}
		
		// 只对 temperature=0 的请求启用缓存
		log.Printf("[缓存检查] temperature=%v, shouldCache=%v", temperature, service.ShouldCache(temperature))
		if service.ShouldCache(temperature) {
			cacheKey = service.GlobalCache.GenerateKey(tokenKey, actualModel, req.Messages, temperature, maxTokens)
			log.Printf("[缓存检查] 生成 cacheKey=%s", cacheKey[:16])
			if cached, found := service.GlobalCache.Get(cacheKey); found {
				log.Printf("[缓存命中] cacheKey=%s", cacheKey[:16])
				duration := time.Since(startTime).Milliseconds()
				c.Header("X-Actual-Model", actualModel)
				c.Header("X-Requested-Model", req.Model)
				c.Header("X-Cache-Hit", "true")
				model.DB.Create(&model.RequestLog{
					TokenName: apiToken.Name, ChannelName: "缓存命中",
					Model: actualModel, RoutedModel: actualModel, StatusCode: 200, DurationMs: duration,
				})
				// 缓存命中也记录限流（防止通过重复请求绕过限流）
				service.RecordRequest(apiToken.ID)
				c.Data(200, "application/json", cached)
				return
			}
		}
	}

	// ===== 工具输出压缩（Phase 2A+ 第二道防线） =====
	// 压缩 role=tool 的消息，减少 token 大户（命令输出/日志/diff）
	{
		compressedTool := service.CompressToolOutput(req.Messages)
		if len(compressedTool) != len(req.Messages) {
			// 长度变了不应该发生，但防御性检查
			log.Printf("[工具压缩] 警告: 消息数量变化 %d → %d", len(req.Messages), len(compressedTool))
		}
		// 即使长度不变，内容可能被压缩了，需要同步
		req.Messages = compressedTool
		var reqMapTC map[string]interface{}
		json.Unmarshal(body, &reqMapTC)
		newMsgs := make([]interface{}, len(req.Messages))
		for i, m := range req.Messages {
			newMsgs[i] = m
		}
		reqMapTC["messages"] = newMsgs
		body, _ = json.Marshal(reqMapTC)
	}

	// ===== 默认行为策略（Phase 2A+ 第一道防线） =====
	// 对开发类任务，从源头注入默认策略，减少确认轮次
	{
		newMsgs, applied := service.ApplyDefaultBehaviorStrategy(req.Messages)
		if applied {
			req.Messages = newMsgs
			var reqMapDB map[string]interface{}
			json.Unmarshal(body, &reqMapDB)
			msgs := make([]interface{}, len(req.Messages))
			for i, m := range req.Messages {
				msgs[i] = m
			}
			reqMapDB["messages"] = msgs
			body, _ = json.Marshal(reqMapDB)
		}
	}

	// ===== 上下文压缩引擎 v2 =====
	// 两级策略：> 100K 无损截断，> 200K 摘要替换
	{
		compressedMsgs := service.CompressContext(req.Messages, tokenKey)
		if len(compressedMsgs) != len(req.Messages) {
			var reqMapCC map[string]interface{}
			json.Unmarshal(body, &reqMapCC)
			newMsgs := make([]interface{}, len(compressedMsgs))
			for i, m := range compressedMsgs {
				newMsgs[i] = m
			}
			reqMapCC["messages"] = newMsgs
			body, _ = json.Marshal(reqMapCC)
			req.Messages = compressedMsgs
		}
	}
	// ===== 行为修正引擎 v2（Phase 2） =====
	// 检测低效对话模式，注入 system hint 减少冗余轮次
	// 安全补丁：用户要求分步确认时跳过（Phase 2C）
	if !service.ShouldSkipBehaviorHint(req.Messages, tokenKey) {
		estTokens := service.EstimateTokensForBehavior(req.Messages)
		hint := service.DetectAndFixBehavior(req.Messages, estTokens)
		if hint != nil {
			req.Messages = service.ApplyBehaviorHint(req.Messages, hint)
			var reqMapBH map[string]interface{}
			json.Unmarshal(body, &reqMapBH)
			newMsgs := make([]interface{}, len(req.Messages))
			for i, m := range req.Messages {
				newMsgs[i] = m
			}
			reqMapBH["messages"] = newMsgs
			body, _ = json.Marshal(reqMapBH)
		}
	}

	result, err := service.RouteRequest(actualModel, body, tokenKey)
	if err != nil {
		// 检查是否是 tool_calls 不兼容错误
		isFastFail := strings.Contains(err.Error(), "快速失败") ||
			strings.Contains(err.Error(), "消息格式错误")
		if isFastFail {
			// tool_calls 错误 → 尝试切到 Qwen（更宽容的 tool_calls 处理）
			if actualModel != "qwen3.7-plus" {
				log.Printf("[路由] tool_calls 不兼容 %s → 尝试 Qwen", actualModel)
				var qwenReqMap map[string]interface{}
				json.Unmarshal(body, &qwenReqMap)
				qwenReqMap["model"] = "qwen3.7-plus"
				// qwen3.7-plus max_tokens 上限 65536
				if mt, ok := qwenReqMap["max_tokens"].(float64); ok && mt > 65536 {
					qwenReqMap["max_tokens"] = 65536
				}
				qwenBody, _ := json.Marshal(qwenReqMap)
				result, err = service.RouteRequest("qwen3.7-plus", qwenBody, tokenKey)
			}
			if err != nil {
				duration := time.Since(startTime).Milliseconds()
				model.DB.Create(&model.RequestLog{
					TokenName: apiToken.Name, ChannelName: "",
					Model: req.Model, StatusCode: 400, DurationMs: duration,
				})
				respondError(c, http.StatusBadRequest, ErrInvalidRequest, err.Error())
				return
			}
			// Qwen 成功了，继续往下走到正常响应
			goto processResult
		}
		// 智能路由降级：尝试备选模型
		// 图片请求只尝试视觉模型（qwen 系列），非视觉模型（DeepSeek）不支持图片
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
processResult:
	// 保存/清除会话模型偏好
	// 有活跃 tool_calls → 缓存模型（保持格式兼容）
	// 无活跃 tool_calls → 清除缓存（即时回落 flash，不等 TTL）
	if service.GlobalModelPref != nil {
		lastRole := ""
		if len(req.Messages) > 0 {
			lastRole, _ = req.Messages[len(req.Messages)-1]["role"].(string)
		}
		hasActiveTC := lastRole == "tool"
		if !hasActiveTC && lastRole == "assistant" {
			if _, ok := req.Messages[len(req.Messages)-1]["tool_calls"]; ok {
				hasActiveTC = true
			}
		}
		if hasActiveTC {
			service.GlobalModelPref.SetPreferredModel(tokenKey, actualModel)
		} else {
			service.GlobalModelPref.ClearPreferredModel(tokenKey)
		}
	}
	defer result.Response.Body.Close()
	// ===== 流式响应分支：逐 chunk 转发 =====
	if isStream {
		duration := time.Since(startTime).Milliseconds()
		model.DB.Create(&model.RequestLog{
			TokenName: apiToken.Name, ChannelName: result.ChannelName, AtmModel: result.AtmModel,
			Model: actualModel, RoutedModel: req.Model, StatusCode: result.Response.StatusCode, DurationMs: duration,
		})
		c.Header("X-Actual-Model", actualModel)
		c.Header("X-Requested-Model", req.Model)
		if result.Response.StatusCode < 500 {
			service.RecordRequest(apiToken.ID)
		}
		// 透传 SSE
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Status(result.Response.StatusCode)
		flusher, hasFlusher := c.Writer.(interface{ Flush() })
		bufReader := bufio.NewReader(result.Response.Body)
		var lastChunk string
		for {
			line, err := bufReader.ReadString('\n')
			if line != "" {
				c.Writer.WriteString(line)
				if hasFlusher {
					flusher.Flush()
				}
				// 记录最后一条非空 data: 行（含 usage 信息）
				if strings.HasPrefix(line, "data: ") && !strings.Contains(line, "[DONE]") {
					lastChunk = strings.TrimPrefix(line, "data: ")
				}
			}
			if err != nil {
				break
			}
		}
		c.Writer.Flush()
		
		// 从流式最后一个 chunk 提取 usage 并记录
		if lastChunk != "" {
			var lastResp struct {
				Usage struct {
					PromptTokens     int64 `json:"prompt_tokens"`
					CompletionTokens int64 `json:"completion_tokens"`
					TotalTokens      int64 `json:"total_tokens"`
					PromptTokensDetails struct {
						CachedTokens int64 `json:"cached_tokens"`
					} `json:"prompt_tokens_details"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(lastChunk), &lastResp); err == nil && lastResp.Usage.TotalTokens > 0 {
				planName := ""
				if apiToken.RateLimitGroup != "" {
					planName = apiToken.RateLimitGroup
				}
				cachedTokens := lastResp.Usage.PromptTokensDetails.CachedTokens
				usageLog := model.UsageLog{
					TokenID:       apiToken.ID,
					TokenName:     apiToken.Name,
					PlanName:      planName,
					ChannelName:   result.ChannelName,
					Model:         actualModel,
					InputTokens:   lastResp.Usage.PromptTokens,
					OutputTokens:  lastResp.Usage.CompletionTokens,
					CachedTokens:  cachedTokens,
					TotalTokens:   lastResp.Usage.TotalTokens,
					EstimatedCost: model.CalculateCost(lastResp.Usage.PromptTokens, lastResp.Usage.CompletionTokens, cachedTokens, actualModel),
					StatusCode:    result.Response.StatusCode,
					DurationMs:    duration,
				}
				model.DB.Create(&usageLog)
				log.Printf("[流式usage] token=%s model=%s tokens=%d cached=%d cost=%.6f",
					apiToken.Name, actualModel, lastResp.Usage.TotalTokens, cachedTokens, usageLog.EstimatedCost)
			}
		}
		
		log.Printf("[流式] token=%s model=%s channel=%s status=%d duration=%dms",
			apiToken.Name, actualModel, result.ChannelName, result.Response.StatusCode, duration)
		return
	}
	// ===== 非流式：原有逻辑 =====
	respBody, _ := io.ReadAll(result.Response.Body)
	duration := time.Since(startTime).Milliseconds()
	model.DB.Create(&model.RequestLog{
		TokenName: apiToken.Name, ChannelName: result.ChannelName, AtmModel: result.AtmModel,
		Model: actualModel, RoutedModel: req.Model, StatusCode: result.Response.StatusCode, DurationMs: duration,
	})
	// 设置响应头：返回实际路由的模型名
	c.Header("X-Actual-Model", actualModel)
	c.Header("X-Requested-Model", req.Model)
	// 请求成功（或至少被上游处理），记录到限流表
	if result.Response.StatusCode < 500 {
		service.RecordRequest(apiToken.ID)
	}
	// 解析 usage 字段并记录用量日志
	if result.Response.StatusCode == 200 {
		var upstreamResp struct {
			Usage struct {
				PromptTokens     int64 `json:"prompt_tokens"`
				CompletionTokens int64 `json:"completion_tokens"`
				TotalTokens      int64 `json:"total_tokens"`
				PromptTokensDetails struct {
					CachedTokens int64 `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
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
			cachedTokens := upstreamResp.Usage.PromptTokensDetails.CachedTokens
			usageLog := model.UsageLog{
				TokenID:       apiToken.ID,
				TokenName:     apiToken.Name,
				PlanName:      planName,
				ChannelID:     channel.ID,
				ChannelName:   result.ChannelName,
				Model:         actualModel,
				InputTokens:   upstreamResp.Usage.PromptTokens,
				OutputTokens:  upstreamResp.Usage.CompletionTokens,
				CachedTokens:  cachedTokens,
				TotalTokens:   upstreamResp.Usage.TotalTokens,
				EstimatedCost: model.CalculateCost(upstreamResp.Usage.PromptTokens, upstreamResp.Usage.CompletionTokens, cachedTokens, actualModel),
				StatusCode:    result.Response.StatusCode,
				DurationMs:    duration,
			}
			model.DB.Create(&usageLog)
			log.Printf("[usage] token=%s model=%s tokens=%d cached=%d cost=%.6f",
				apiToken.Name, actualModel, upstreamResp.Usage.TotalTokens, cachedTokens, usageLog.EstimatedCost)
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
		cost := model.CalculateCost(r.InputTokens, r.OutputTokens, 0, r.Model)
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
		todayCost += model.CalculateCost(r.Input, r.Output, 0, r.Model)
	}
	var weekRows []AllRow
	model.DB.Raw(`SELECT input_tokens as input, output_tokens as output, model FROM usage_logs
		WHERE created_at >= datetime('now', '-7 days', 'localtime')`).Scan(&weekRows)
	for _, r := range weekRows {
		weekCost += model.CalculateCost(r.Input, r.Output, 0, r.Model)
	}
	var monthRows []AllRow
	model.DB.Raw(`SELECT input_tokens as input, output_tokens as output, model FROM usage_logs
		WHERE created_at >= datetime('now', '-30 days', 'localtime')`).Scan(&monthRows)
	for _, r := range monthRows {
		monthCost += model.CalculateCost(r.Input, r.Output, 0, r.Model)
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
		cost := model.CalculateCost(r.InputTokens, r.OutputTokens, 0, "default")
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
		cost := model.CalculateCost(r.InputTokens, r.OutputTokens, 0, "default")
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
	// 按 token 套餐过滤：只返回当前套餐允许的模型
	var allowedList []string

	// 提取 token
	tokenKey := extractToken(c)
	if tokenKey != "" {
		if apiToken, err := model.FindByKey(tokenKey); err == nil && apiToken.ID > 0 {
			// 用 plan_name 查找套餐（优先），其次是 rate_limit_group
			planName := apiToken.PlanName
			if planName == "" {
				planName = apiToken.RateLimitGroup
			}
			if planName != "" {
				allowedList = service.GetAllowedModels(planName)
			}
		}
	}

	// 如果没有 token 或套餐未配置，回退到通用 deepseek-a4 列表
	if len(allowedList) == 0 {
		// 不鉴权时只显示 deepseek-a4（统一入口模型）
		allowedList = []string{"deepseek-a4"}
	}

	// 如果 allowed_models 包含 deepseek-a4，补充其实际后端模型（方便客户端理解）
	hasA4 := false
	for _, m := range allowedList {
		if m == "deepseek-a4" {
			hasA4 = true
			break
		}
	}
	if hasA4 {
		// deepseek-a4 的下游模型不直接暴露，deepseek-a4 作为统一入口
		// 只保留 deepseek-a4，不放具体后端
	}

	c.JSON(http.StatusOK, gin.H{"data": allowedList})
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
// publicStats 公开统计接口（无需登录，给监控中心 iframe 用）
func publicStats(c *gin.Context) {
	type DailyStat struct {
		Date   string `json:"date"`
		Count  int64  `json:"count"`
		Errors int64  `json:"errors"`
	}
	var dailyStats []DailyStat
	model.DB.Raw(`SELECT date(created_at) as date, count(*) as count,
		SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END) as errors
		FROM request_logs WHERE created_at > datetime('now', '-7 days')
		GROUP BY date(created_at) ORDER BY date DESC`).Scan(&dailyStats)
	var totalCount, totalErrors int64
	model.DB.Model(&model.RequestLog{}).Count(&totalCount)
	model.DB.Model(&model.RequestLog{}).Where("status_code >= 400").Count(&totalErrors)
	var avgDuration int64
	model.DB.Raw("SELECT CAST(AVG(duration_ms) AS INTEGER) FROM request_logs").Scan(&avgDuration)
	// 今日统计
	var todayCount, todayErrors int64
	model.DB.Model(&model.RequestLog{}).Where("date(created_at)=date('now','localtime')").Count(&todayCount)
	model.DB.Model(&model.RequestLog{}).Where("date(created_at)=date('now','localtime') AND status_code >= 400").Count(&todayErrors)
	// 活跃 token 数
	var activeTokens, totalTokens int64
	model.DB.Model(&model.Token{}).Where("status = 1").Count(&activeTokens)
	model.DB.Model(&model.Token{}).Count(&totalTokens)
	// 模型统计
	type ModelStat struct {
		Model       string `json:"model"`
		Count       int64  `json:"count"`
		TotalTokens int64  `json:"total_tokens"`
	}
	var modelStats []ModelStat
	model.DB.Raw(`SELECT model, count(*) as count, coalesce(sum(total_tokens),0) as total_tokens
		FROM usage_logs GROUP BY model ORDER BY count DESC LIMIT 10`).Scan(&modelStats)
	// 最近请求
	type RecentLog struct {
		CreatedAt  string `json:"created_at"`
		TokenName  string `json:"token_name"`
		Model      string `json:"model"`
		ChannelName string `json:"channel_name"`
		StatusCode int    `json:"status_code"`
		DurationMs int64  `json:"duration_ms"`
	}
	var recent []RecentLog
	model.DB.Raw(`SELECT created_at, token_name, model, channel_name, status_code, duration_ms
		FROM request_logs ORDER BY id DESC LIMIT 20`).Scan(&recent)
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"total_requests": totalCount,
		"total_errors":  totalErrors,
		"today_count":   todayCount,
		"today_errors":  todayErrors,
		"avg_duration":  avgDuration,
		"active_tokens": activeTokens,
		"total_tokens":  totalTokens,
		"daily":         dailyStats,
		"by_model":      modelStats,
		"recent":        recent,
	}})
}
// getTokenUsage OpenAI 兼容的用量查询 API
// GET /v1/usage
// Authorization: Bearer atm-xxx
func getTokenUsage(c *gin.Context) {
	tokenKey := extractToken(c)
	if tokenKey == "" {
		respondError(c, http.StatusUnauthorized, ErrUnauthorized, "缺少认证 token")
		return
	}
	apiToken, err := model.FindByKey(tokenKey)
	if err != nil {
		respondError(c, http.StatusUnauthorized, ErrTokenNotFound, "token 不存在")
		return
	}
	now := time.Now().Unix()
	rlResult := service.CheckRateLimit(apiToken)
	// 状态判断
	status := "active"
	if apiToken.Status == 2 {
		status = "disabled"
	} else if apiToken.ExpiredTime > 0 && now > apiToken.ExpiredTime {
		status = "expired"
	}
	// 过期时间
	var expireDate string
	var remainingDays int
	if apiToken.ExpiredTime > 0 {
		remainingDays = int((apiToken.ExpiredTime - now) / 86400)
		if remainingDays < 0 {
			remainingDays = 0
		}
		expireDate = time.Unix(apiToken.ExpiredTime, 0).Format("2006-01-02 15:04:05")
	} else {
		expireDate = "never"
		remainingDays = -1
	}
	c.JSON(http.StatusOK, gin.H{
		"token_name":    apiToken.Name,
		"plan":          apiToken.RateLimitGroup,
		"status":        status,
		"quota_5h": gin.H{
			"used":  rlResult.Used5h,
			"limit": rlResult.Limit5h,
			"remaining": rlResult.Limit5h - rlResult.Used5h,
		},
		"quota_daily": gin.H{
			"used":  rlResult.UsedDaily,
			"limit": rlResult.LimitDaily,
			"remaining": rlResult.LimitDaily - rlResult.UsedDaily,
		},
		"quota_weekly": gin.H{
			"used":  rlResult.UsedWeekly,
			"limit": rlResult.LimitWeekly,
			"remaining": rlResult.LimitWeekly - rlResult.UsedWeekly,
		},
		"quota_monthly": gin.H{
			"used":  rlResult.UsedMonthly,
			"limit": rlResult.LimitMonthly,
			"remaining": rlResult.LimitMonthly - rlResult.UsedMonthly,
		},
		"expired_at":     expireDate,
		"remaining_days": remainingDays,
	})
}
func tokenInfo(c *gin.Context) {
	tokenKey := c.Query("token")
	if tokenKey == "" {
		// 返回 200 而非 400，避免 QQ 内置浏览器把 4xx 当网络错误
		c.JSON(http.StatusOK, gin.H{"error": gin.H{"code": "INVALID_REQUEST", "message": "请提供 token"}})
		return
	}
	token, err := model.FindByKey(tokenKey)
	if err != nil {
		// 返回 200 而非 404，避免 QQ/微信内置浏览器 fetch 抛异常
		c.JSON(http.StatusOK, gin.H{"error": gin.H{"code": "TOKEN_NOT_FOUND", "message": "token 不存在"}})
		return
	}
	// 计算使用情况
	now := time.Now().Unix()
	fiveHoursAgo := now - 5*3600
	oneDayAgo := now - 24*3600
	sevenDaysAgo := now - 7*24*3600
	thirtyDaysAgo := now - 30*24*3600
	var count5h, countDaily, count7d, count30d int64
	model.DB.Model(&model.RateLimit{}).Where("token_id = ? AND request_time > ?", token.ID, fiveHoursAgo).Count(&count5h)
	model.DB.Model(&model.RateLimit{}).Where("token_id = ? AND request_time > ?", token.ID, oneDayAgo).Count(&countDaily)
	model.DB.Model(&model.RateLimit{}).Where("token_id = ? AND request_time > ?", token.ID, sevenDaysAgo).Count(&count7d)
	model.DB.Model(&model.RateLimit{}).Where("token_id = ? AND request_time > ?", token.ID, thirtyDaysAgo).Count(&count30d)
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
	var limit5h, dailyMax, weeklyMax, monthlyMax, maxQPS int64
	var skipHourly bool
	if token.RateLimitGroup != "" {
		var plan model.Plan
		if err := model.DB.Where("name = ?", token.RateLimitGroup).First(&plan).Error; err == nil {
			limit5h = plan.Hourly5Max
			dailyMax = plan.DailyMax
			weeklyMax = plan.WeeklyMax
			monthlyMax = plan.MonthlyMax
			maxQPS = plan.MaxQPS
			skipHourly = plan.SkipHourly
			planDisplayName = plan.DisplayName
			planDesc = plan.Description
		}
	}
	// 状态判断
	status := "active"
	if token.Status == 2 {
		status = "disabled"
	} else if token.ExpiredTime > 0 && now > token.ExpiredTime {
		status = "expired"
	} else if token.ActivatedAt == 0 {
		status = "waiting"
	}
	// 计算剩余时间
	var remainingDays int
	var expireDate string
	if token.ExpiredTime > 0 {
		remainingDays = int((token.ExpiredTime - now) / 86400)
		if remainingDays < 0 {
			remainingDays = 0
		}
		expireDate = time.Unix(token.ExpiredTime, 0).Format("2006-01-02 15:04:05")
	} else {
		remainingDays = -1
		expireDate = "永不过期"
	}
	// 获取所有套餐列表（供前端升级选择）
	var allPlans []model.Plan
	model.DB.Order("CAST(price AS REAL)").Find(&allPlans)
	type PlanBrief struct {
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
		Price       string `json:"price"`
		Hourly5Max  int64  `json:"hourly_5_max"`
		MonthlyMax  int64  `json:"monthly_max"`
	}
	allPlansList := make([]PlanBrief, len(allPlans))
	planPrice := ""
	for i, p := range allPlans {
		allPlansList[i] = PlanBrief{p.Name, p.DisplayName, p.Price, p.Hourly5Max, p.MonthlyMax}
		if p.Name == token.RateLimitGroup {
			planPrice = p.Price
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"status":        status,
		"token_name":    token.Name,
		"plan":          token.RateLimitGroup,
		"plan_name":     planDisplayName,
		"plan_desc":     planDesc,
		"plan_price":    planPrice,
		"all_plans":     allPlansList,
		"skip_hourly":   skipHourly,
		"limit_5h":      limit5h,
		"used_5h":       count5h,
		"remaining_5h":  limit5h - count5h,
		"limit_daily":   dailyMax,
		"used_daily":    countDaily,
		"weekly_max":    weeklyMax,
		"used_7d":       count7d,
		"monthly_max":   monthlyMax,
		"monthly_used":  count30d,
		"max_qps":       maxQPS,
		"total_calls":   total.Calls,
		"total_tokens":  total.Toks,
		"week_calls":    week.Calls,
		"week_tokens":   week.Toks,
		"activated_at": func() string {
			if token.ActivatedAt == 0 {
				return "未激活"
			}
			return time.Unix(token.ActivatedAt, 0).Format("2006-01-02 15:04:05")
		}(),
		"expired_at":     expireDate,
		"remaining_days": remainingDays,
	})
}
// ===== 辅助函数 =====
// 注册限流：每 IP 每分钟最多 3 次注册
var registerRateLimit = struct {
	sync.RWMutex
	records map[string][]time.Time
}{records: make(map[string][]time.Time)}
func checkRegisterRateLimit(ip string) bool {
	registerRateLimit.Lock()
	defer registerRateLimit.Unlock()
	now := time.Now()
	oneMinuteAgo := now.Add(-time.Minute)
	// 清理过期记录
	if records, exists := registerRateLimit.records[ip]; exists {
		valid := make([]time.Time, 0)
		for _, t := range records {
			if t.After(oneMinuteAgo) {
				valid = append(valid, t)
			}
		}
		registerRateLimit.records[ip] = valid
	}
	// 检查是否超限
	if len(registerRateLimit.records[ip]) >= 3 {
		return false
	}
	// 记录本次
	registerRateLimit.records[ip] = append(registerRateLimit.records[ip], now)
	return true
}
// 登录限流：每 IP 每分钟最多 10 次登录
var loginRateLimit = struct {
	sync.RWMutex
	records map[string][]time.Time
}{records: make(map[string][]time.Time)}
func checkLoginRateLimit(ip string) bool {
	loginRateLimit.Lock()
	defer loginRateLimit.Unlock()
	now := time.Now()
	oneMinuteAgo := now.Add(-time.Minute)
	// 清理过期记录
	if records, exists := loginRateLimit.records[ip]; exists {
		valid := make([]time.Time, 0)
		for _, t := range records {
			if t.After(oneMinuteAgo) {
				valid = append(valid, t)
			}
		}
		loginRateLimit.records[ip] = valid
	}
	// 检查是否超限
	if len(loginRateLimit.records[ip]) >= 10 {
		return false
	}
	// 记录本次
	loginRateLimit.records[ip] = append(loginRateLimit.records[ip], now)
	return true
}
func generateTokenKey() string {
	return service.GenerateAPIKey()
}

// batchCreateTokens 批量生产 token
func batchCreateTokens(c *gin.Context) {
	var req struct {
		PlanGroup  string `json:"plan_group" binding:"required"`
		PlanName   string `json:"plan_name" binding:"required"`
		Count      int    `json:"count" binding:"required,min=1,max=100"`
		ExpireDays int    `json:"expire_days"`
		NamePrefix string `json:"name_prefix"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ExpireDays <= 0 {
		req.ExpireDays = 30
	}

	var plan model.Plan
	if err := model.DB.Where("name = ?", req.PlanName).First(&plan).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("套餐 %s 不存在", req.PlanName)})
		return
	}

	expiredTime := int64(0)
	if req.ExpireDays > 0 {
		expiredTime = time.Now().AddDate(0, 0, req.ExpireDays).Unix()
	}
	activatedAt := time.Now().Unix()

	type Result struct {
		Key         string `json:"key"`
		PlanDisplay string `json:"plan_display"`
		ExpiredAt   string `json:"expired_at"`
	}
	var results []Result

	for i := 0; i < req.Count; i++ {
		key := generateTokenKey()
		name := fmt.Sprintf("%s-%s-%03d", req.PlanGroup, req.PlanName, i+1)

		token := model.Token{
			UserID:         0,
			Name:           name,
			Key:            key,
			Status:         1,
			RemainQuota:    plan.MonthlyMax,
			InitQuota:      plan.MonthlyMax,
			UnlimitedQuota: false,
			ExpiredTime:    expiredTime,
			ActivatedAt:    activatedAt,
			CreatedTime:    activatedAt,
			RateLimitGroup: req.PlanName,
			PlanGroup:      req.PlanGroup,
			PlanName:       req.PlanName,
		}
		if err := model.DB.Create(&token).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败", "index": i})
			return
		}

		expiredStr := "永久"
		if expiredTime > 0 {
			expiredStr = time.Unix(expiredTime, 0).Format("2006-01-02")
		}

		results = append(results, Result{
			Key:         key,
			PlanDisplay: plan.DisplayName,
			ExpiredAt:   expiredStr,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"message":  fmt.Sprintf("成功生产 %d 个 token", len(results)),
		"tokens":   results,
		"plan":     plan.DisplayName,
		"quota":    plan.MonthlyMax,
	})
}

// compressLongContext 已迁移到 service/context_compressor.go（CompressContext）

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
func containsString(slice []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, s := range slice {
		if strings.ToLower(strings.TrimSpace(s)) == target {
			return true
		}
	}
	return false
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
// ===== 支付相关（已迁移到 payment_handler.go）=====
// createOrder    → payment_handler.go
// alipayNotify   → payment_handler.go
// wechatNotify   → payment_handler.go
// getOrderStatus → payment_handler.go
// getPayments    → payment_handler.go
// refundPayment  → payment_handler.go
// stripMetadata 过滤 OpenClaw 图片消息的元数据头
func stripMetadata(s string) string {
	// 去掉 ```json ... ``` 块
	for {
		idx := strings.Index(s, "```json")
		if idx < 0 { break }
		end := strings.Index(s[idx:], "```\n")
		if end < 0 { break }
		s = s[:idx] + s[idx+end+4:]
	}
	// 去掉元数据标签行
	lines := strings.Split(s, "\n")
	var clean []string
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" { continue }
		l := strings.ToLower(t)
		if strings.Contains(l, "untrusted metadata") ||
		   strings.Contains(l, "conversation info") ||
		   strings.Contains(l, "sender (") ||
		   strings.Contains(l, "chat_id") ||
		   strings.Contains(l, "message_id") ||
		   strings.Contains(l, "sender_id") ||
		   strings.Contains(l, "inbound") ||
		   strings.Contains(l, "timestamp") ||
		   strings.Contains(l, "channel_account") {
			continue
		}
		clean = append(clean, line)
	}
	return strings.TrimSpace(strings.Join(clean, "\n"))
}
// hasOpenClawImageMetadata 检查消息中是否有 OpenClaw 图片元数据标记
// OpenClaw 发图时 text 内容是 Conversation info + Sender + [media attached:] 等元数据
func hasOpenClawImageMetadata(messages []map[string]interface{}) bool {
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		if role != "user" { continue }
		content := msg["content"]
		switch c := content.(type) {
		case string:
			if strings.Contains(c, "[media attached:") || strings.Contains(c, "- Images:") {
				return true
			}
		case []interface{}:
			for _, part := range c {
				if partMap, ok := part.(map[string]interface{}); ok {
					if typ, _ := partMap["type"].(string); typ == "text" {
						if text, ok := partMap["text"].(string); ok {
							if strings.Contains(text, "[media attached:") || strings.Contains(text, "- Images:") {
										dbgLog("[IMG-DBG] FOUND media tag, caching")
								return true
							}
						}
					}
				}
			}
		}
	}
	return false
}
// extractUserQuestion 提取最后一条 user 消息中的实质性问题
// 过滤掉 OpenClaw 元数据（[media attached:], Conversation info 等）
func extractUserQuestion(messages []map[string]interface{}) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if role, _ := messages[i]["role"].(string); role != "user" {
			continue
		}
		content := messages[i]["content"]
		switch c := content.(type) {
		case string:
			if strings.HasPrefix(c, "data:image") {
				continue
			}
			// 如果包含图片元数据，尝试提取文字部分
			if strings.Contains(c, "[media attached:") || strings.Contains(c, "- Images:") {
				// 去掉元数据行，提取用户文字
				lines := strings.Split(c, "\n")
				var textLines []string
				for _, line := range lines {
					l := strings.TrimSpace(line)
					if l == "" {
						continue
					}
					if strings.HasPrefix(l, "[media attached:") || strings.HasPrefix(l, "- Images:") {
						continue
					}
					textLines = append(textLines, line)
				}
				text := strings.TrimSpace(strings.Join(textLines, "\n"))
				if text != "" {
					return text
				}
				continue // 没有文字，跳过这条消息
			}
			return strings.TrimSpace(c)
		case []interface{}:
			// 找最后一个不含元数据的 text（图片+文字混合时，用户文字通常在最后）
			var lastText string
			for _, part := range c {
				if pm, ok := part.(map[string]interface{}); ok {
					if typ, _ := pm["type"].(string); typ == "text" {
						if text, ok := pm["text"].(string); ok {
							log.Printf("[extractUserQuestion] processing text part, len=%d", len(text))
							// 如果包含媒体标记或元数据，提取用户文字
							if strings.Contains(text, "[media attached:") || strings.Contains(text, "- Images:") ||
								strings.Contains(text, "Conversation info") || strings.Contains(text, "Sender (untrusted") {
								lines := strings.Split(text, "\n")
								var textLines []string
								skipBlock := false
								for _, line := range lines {
									l := strings.TrimSpace(line)
									if l == "" {
										continue
									}
									// 跳过媒体标记行
									if strings.HasPrefix(l, "[media attached:") || strings.HasPrefix(l, "- Images:") {
										continue
									}
									// 跳过 Conversation info 块
									if strings.HasPrefix(l, "Conversation info") {
										skipBlock = true
										continue
									}
									// 跳过 Sender 块
									if strings.HasPrefix(l, "Sender (untrusted") {
										skipBlock = true
										continue
									}
									// 跳过 JSON 代码块
									if strings.HasPrefix(l, "```json") || strings.HasPrefix(l, "```") {
										continue
									}
									// 跳过 JSON 对象行（以 { 或 } 开头）
									if strings.HasPrefix(l, "{") || strings.HasPrefix(l, "}") || strings.HasPrefix(l, "\"") {
										continue
									}
									// 如果在跳过块模式，继续跳过
									if skipBlock {
										// 检查是否块结束（遇到非 JSON 行）
										if !strings.HasPrefix(l, "{") && !strings.HasPrefix(l, "}") && !strings.HasPrefix(l, "\"") {
											skipBlock = false
										} else {
											continue
										}
									}
									textLines = append(textLines, line)
								}
								extracted := strings.TrimSpace(strings.Join(textLines, "\n"))
								log.Printf("[extractUserQuestion] extracted text: %q", extracted[:min(100, len(extracted))])
								if extracted != "" {
									lastText = extracted
								}
							} else {
								lastText = text
							}
						}
					}
				}
			}
			if lastText != "" {
				return strings.TrimSpace(lastText)
			}
		}
	}
	return ""
}

// ===== Phase 2C 成本仪表盘 API =====
func getDashboard(c *gin.Context) {
	period := c.DefaultQuery("period", "today")
	var startTime, endTime time.Time

	now := time.Now()
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

	summary, err := service.GetDashboardSummary(startTime, endTime)
	if err != nil {
		respondError(c, http.StatusInternalServerError, ErrInternal, "获取仪表盘数据失败: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": summary, "period": period})
}

func getTokenCost(c *gin.Context) {
	idStr := c.Param("id")
	tokenID, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest, "无效的 token ID")
		return
	}

	period := c.DefaultQuery("period", "today")
	var startTime, endTime time.Time

	now := time.Now()
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

	summary, err := service.GetTokenCostSummary(uint(tokenID), startTime, endTime)
	if err != nil {
		respondError(c, http.StatusInternalServerError, ErrInternal, "获取 token 成本失败: "+err.Error())
		return
	}

	// 检查是否亏损
	isLoss, profit, _ := service.CheckTokenLoss(uint(tokenID))

	c.JSON(http.StatusOK, gin.H{
		"data":       summary,
		"period":     period,
		"is_loss":    isLoss,
		"profit":     profit,
	})
}
