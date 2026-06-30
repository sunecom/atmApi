package api

import (
	"fmt"
	"net/http"
	"time"

	"atmapi/internal/model"

	"github.com/gin-gonic/gin"
)

// masCreateToken 创建学员 MaaS Token
func masCreateToken(c *gin.Context) {
	var req struct {
		StudentID string `json:"student_id" binding:"required"`
		Tier      string `json:"tier" binding:"required"`
		NotifyURL string `json:"notify_url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "error": err.Error()})
		return
	}

	cfg, ok := model.MASTierConfigs[req.Tier]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "error": "无效的套餐级别，支持: trial/starter/pro/max/unlimited"})
		return
	}

	token := model.MASToken{
		Token:            model.GenerateMASToken(req.Tier),
		Tier:             req.Tier,
		TierName:         cfg.Name,
		StudentID:        req.StudentID,
		Quota:            cfg.Quota,
		Used:             0,
		Remaining:        cfg.Quota,
		ExpiresAt:        time.Now().Add(time.Duration(cfg.ExpiresHours) * time.Hour),
		Models:           cfg.Models,
		ConcurrencyLimit: cfg.ConcurrencyLimit,
		Status:           1,
		DefaultModel:     cfg.DefaultModel,
		AlertThreshold:   cfg.AlertThreshold,
		NotifyURL:        req.NotifyURL,
	}

	if err := model.DB.Create(&token).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "error": "创建失败"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"code": 0,
		"data": gin.H{
			"token":         token.Token,
			"tier":          token.Tier,
			"tier_name":     token.TierName,
			"quota":         token.Quota,
			"expires_at":    token.ExpiresAt.Format(time.RFC3339),
			"base_url":      "http://8.220.139.36:3002/v1",
			"default_model": token.DefaultModel,
			"models":        token.AllowedModels(),
		},
	})
}

// masRenewal Token 续费/升级
func masRenewal(c *gin.Context) {
	tokenStr := c.Param("token")
	var req struct {
		NewTier    string `json:"new_tier"`
		ExtendDays int64  `json:"extend_days"`
	}
	c.ShouldBindJSON(&req)

	var maToken model.MASToken
	if err := model.DB.Where("token = ?", tokenStr).First(&maToken).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "error": "Token 不存在"})
		return
	}

	if req.NewTier != "" {
		cfg, ok := model.MASTierConfigs[req.NewTier]
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1, "error": "无效的套餐级别"})
			return
		}
		now := time.Now()
		updates := map[string]interface{}{
			"tier":              req.NewTier,
			"tier_name":         cfg.Name,
			"quota":             cfg.Quota,
			"remaining":         cfg.Quota,
			"used":              0,
			"expires_at":        now.Add(time.Duration(cfg.ExpiresHours) * time.Hour),
			"models":            cfg.Models,
			"default_model":     cfg.DefaultModel,
			"concurrency_limit": cfg.ConcurrencyLimit,
			"alert_threshold":   cfg.AlertThreshold,
			"status":            1,
			"renewal_count":     maToken.RenewalCount + 1,
		}
		model.DB.Model(&maToken).Updates(updates)
		maToken.Tier = req.NewTier
		maToken.Quota = cfg.Quota
		maToken.Remaining = cfg.Quota
		maToken.Used = 0
		maToken.Status = 1
	} else if req.ExtendDays > 0 {
		updates := map[string]interface{}{
			"expires_at":    maToken.ExpiresAt.Add(time.Duration(req.ExtendDays) * 24 * time.Hour),
			"remaining":     maToken.Quota,
			"used":          0,
			"status":        1,
			"renewal_count": maToken.RenewalCount + 1,
		}
		model.DB.Model(&maToken).Updates(updates)
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"token":         maToken.Token,
			"tier":          maToken.Tier,
			"quota":         maToken.Quota,
			"remaining":     maToken.Remaining,
			"expires_at":    maToken.ExpiresAt.Format(time.RFC3339),
			"renewal_count": maToken.RenewalCount,
		},
	})
}

// masUsage 用量查询
func masUsage(c *gin.Context) {
	tokenStr := c.Param("token")
	var maToken model.MASToken
	if err := model.DB.Where("token = ?", tokenStr).First(&maToken).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "error": "Token 不存在"})
		return
	}

	maToken.CheckAndSetStatus()

	usagePct := 0
	if maToken.Quota > 0 {
		usagePct = int(float64(maToken.Used) / float64(maToken.Quota) * 100)
	}

	daysRem := int(time.Until(maToken.ExpiresAt).Hours() / 24)
	if daysRem < 0 {
		daysRem = 0
	}

	statusMap := map[int]string{1: "active", 2: "exhausted", 3: "expired", 4: "banned"}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"token":          maToken.Token,
			"tier":           maToken.Tier,
			"tier_name":      maToken.TierName,
			"quota":          maToken.Quota,
			"used":           maToken.Used,
			"remaining":      maToken.Remaining,
			"usage_percent":  usagePct,
			"expires_at":     maToken.ExpiresAt.Format(time.RFC3339),
			"days_remaining": daysRem,
			"status":         statusMap[maToken.Status],
			"default_model":  maToken.DefaultModel,
			"models":         maToken.AllowedModels(),
			"concurrency":    maToken.ConcurrencyLimit,
			"renewal_count":  maToken.RenewalCount,
		},
	})
}

// masList 列出所有 MaaS Token（管理后台用）
func masList(c *gin.Context) {
	var tokens []model.MASToken
	query := model.DB

	// 支持筛选
	if tier := c.Query("tier"); tier != "" {
		query = query.Where("tier = ?", tier)
	}
	if status := c.Query("status"); status != "" {
		query = query.Where("status = ?", status)
	}
	if studentID := c.Query("student_id"); studentID != "" {
		query = query.Where("student_id = ?", studentID)
	}

	query.Order("created_at DESC").Find(&tokens)

	// 自动更新状态
	for i := range tokens {
		tokens[i].CheckAndSetStatus()
		if tokens[i].Status != 1 && tokens[i].Status != 2 && tokens[i].Status != 3 {
			// 保持原有状态
		}
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": tokens, "total": len(tokens)})
}

// masTiers 获取套餐列表
func masTiers(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": model.MASTierConfigs})
}

// masBan 封禁 Token
func masBan(c *gin.Context) {
	tokenStr := c.Param("token")
	if err := model.DB.Model(&model.MASToken{}).Where("token = ?", tokenStr).Update("status", 4).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "error": "Token 不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "已封禁"})
}

// masUnban 解封 Token
func masUnban(c *gin.Context) {
	tokenStr := c.Param("token")
	if err := model.DB.Model(&model.MASToken{}).Where("token = ?", tokenStr).Updates(map[string]interface{}{"status": 1, "remaining": 0}).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "error": "Token 不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "已解封，请手动设置剩余配额"})
}

// MaaS 响应头中间件 — 在 chatCompletions 中注入
func injectMAHeaders(c *gin.Context) {
	tokenKey := extractToken(c)
	if tokenKey == "" || len(tokenKey) < 5 || tokenKey[:5] != "sk-m-" {
		c.Next()
		return
	}
	var maToken model.MASToken
	if err := model.DB.Where("token = ?", tokenKey).First(&maToken).Error; err != nil {
		c.Next()
		return
	}
	c.Header("X-MAS-Status", "active")
	if maToken.Quota < 0 {
		c.Header("X-MAS-Remaining", "unlimited")
	} else {
		c.Header("X-MAS-Remaining", fmt.Sprintf("%d", maToken.Remaining))
	}
	c.Header("X-MAS-Tier", maToken.Tier)
	c.Next()
}