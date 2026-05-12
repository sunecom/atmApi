package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"atmapi/internal/middleware"
	"atmapi/internal/model"
	"atmapi/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes 注册所有路由
func RegisterRoutes(r *gin.Engine) {
	// CORS 中间件
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

	// 静态页面
	r.Static("/static", "./web/static")
	r.GET("/", indexPage)

	// 健康检查
	r.GET("/health", healthCheck)

	// API v1 路由组
	v1 := r.Group("/api/v1")
	{
		// 认证（无需登录）
		v1.POST("/login", login)
		v1.POST("/register", register)

		// 公开接口
		v1.GET("/models", listModels)

		// 需要认证的管理接口
		managed := v1.Group("")
		managed.Use(middleware.AuthRequired())
		{
			// Token 管理
			managed.GET("/tokens", getTokens)
			managed.POST("/tokens", createToken)
			managed.PUT("/tokens/:id", updateToken)
			managed.DELETE("/tokens/:id", deleteToken)

			// 渠道管理
			managed.GET("/channels", getChannels)
			managed.POST("/channels", createChannel)
			managed.PUT("/channels/:id", updateChannel)
			managed.DELETE("/channels/:id", deleteChannel)

			// 请求日志
			managed.GET("/logs", getLogs)

			// 模型路由
			managed.POST("/chat/completions", chatCompletions)
		}
	}
}

// 首页
func indexPage(c *gin.Context) {
	c.File("./web/static/index.html")
}

// 健康检查
func healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"version": "0.1.0",
		"time":    time.Now().Format("2006-01-02 15:04:05"),
	})
}

// 登录
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
	result := model.DB.Where("username = ? AND password = ? AND status = ?", req.Username, req.Password, 1).First(&user)
	if result.Error != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}

	// 生成 JWT
	token, err := middleware.GenerateToken(user.ID, user.Username, user.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成 token 失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "登录成功",
		"token":        token,
		"user_id":      user.ID,
		"username":     user.Username,
		"display_name": user.DisplayName,
		"role":         user.Role,
	})
}

// 注册
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

	// 检查用户名是否已存在
	var count int64
	model.DB.Model(&model.User{}).Where("username = ?", req.Username).Count(&count)
	if count > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "用户名已存在"})
		return
	}

	user := model.User{
		Username:    req.Username,
		Password:    req.Password,
		DisplayName: req.DisplayName,
		Email:       req.Email,
		Role:        1,
		Status:      1,
		Quota:       100000, // 默认配额
	}

	if err := model.DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "注册失败"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "注册成功",
		"user_id": user.ID,
	})
}

// 获取 Token 列表
func getTokens(c *gin.Context) {
	var tokens []model.Token
	model.DB.Find(&tokens)
	c.JSON(http.StatusOK, gin.H{"data": tokens})
}

// 创建 Token
func createToken(c *gin.Context) {
	var req struct {
		UserID         uint   `json:"user_id" binding:"required"`
		Name           string `json:"name" binding:"required"`
		RemainQuota    int64  `json:"remain_quota"`
		UnlimitedQuota bool   `json:"unlimited_quota"`
		ExpiredTime    int64  `json:"expired_time"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 生成随机 token key
	key := generateTokenKey()

	token := model.Token{
		UserID:         req.UserID,
		Name:           req.Name,
		Key:            key,
		Status:         1,
		RemainQuota:    req.RemainQuota,
		UnlimitedQuota: req.UnlimitedQuota,
		ExpiredTime:    req.ExpiredTime,
		CreatedTime:    time.Now().Unix(),
	}

	if err := model.DB.Create(&token).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Token 创建成功",
		"token":   token,
	})
}

// 更新 Token
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

	if err := model.DB.Model(&token).Updates(req).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "更新成功", "token": token})
}

// 删除 Token
func deleteToken(c *gin.Context) {
	id := c.Param("id")
	if err := model.DB.Delete(&model.Token{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}

// 获取渠道列表
func getChannels(c *gin.Context) {
	var channels []model.Channel
	model.DB.Order("priority DESC, weight DESC").Find(&channels)
	c.JSON(http.StatusOK, gin.H{"data": channels})
}

// 创建渠道
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

// 更新渠道
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

// 删除渠道
func deleteChannel(c *gin.Context) {
	id := c.Param("id")
	if err := model.DB.Delete(&model.Channel{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}

// 模型路由（核心功能）- 带请求日志
func chatCompletions(c *gin.Context) {
	startTime := time.Now()

	// 获取 token
	tokenKey := extractToken(c)
	if tokenKey == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少认证 token"})
		return
	}

	// 查找 token 信息（用于日志）
	var apiToken model.Token
	model.DB.Where("key = ?", tokenKey).First(&apiToken)

	// 读取请求体
	body, err := service.ReadBody(c.Request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求失败"})
		return
	}

	// 解析请求获取目标模型
	var req struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}

	// 路由请求
	resp, err := service.RouteRequest(req.Model, body, tokenKey)
	if err != nil {
		// 记录失败日志
		duration := time.Since(startTime).Milliseconds()
		model.DB.Create(&model.RequestLog{
			TokenName:   apiToken.Name,
			ChannelName: "无可用渠道",
			Model:       req.Model,
			StatusCode:  502,
			DurationMs:  duration,
		})
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	// 读取上游响应
	respBody, _ := io.ReadAll(resp.Body)

	// 记录成功日志
	duration := time.Since(startTime).Milliseconds()
	model.DB.Create(&model.RequestLog{
		TokenName:   apiToken.Name,
		ChannelName: "已路由", // service 层可优化为返回实际渠道名
		Model:       req.Model,
		StatusCode:  resp.StatusCode,
		DurationMs:  duration,
	})

	// 返回上游响应
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}

// 获取请求日志
func getLogs(c *gin.Context) {
	var logs []model.RequestLog
	model.DB.Order("id DESC").Limit(100).Find(&logs)
	c.JSON(http.StatusOK, gin.H{"data": logs})
}

// 列出可用模型
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

// 辅助函数
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
