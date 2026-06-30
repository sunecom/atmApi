package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"atmapi/internal/model"

	"github.com/gin-gonic/gin"
)

var (
	maConcurrent = make(map[uint]int)
	maConcMu     sync.Mutex
)

// MASTokenAuth MaaS Token 认证 + 用量拦截中间件
// 拦截所有以 sk-m- 开头的 Token，进行配额/过期/并发控制
func MASTokenAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenKey := extractBearerTokenMA(c)
		if tokenKey == "" || !strings.HasPrefix(tokenKey, "sk-m-") {
			c.Next()
			return
		}

		var maToken model.MASToken
		if err := model.DB.Where("token = ?", tokenKey).First(&maToken).Error; err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "无效的 MaaS Token"})
			c.Abort()
			return
		}

		maToken.CheckAndSetStatus()

		if maToken.Status != 1 {
			var msg string
			switch maToken.Status {
			case 2:
				msg = "配额已用尽，请续费"
			case 3:
				msg = "套餐已过期，请续费"
			case 4:
				msg = "账号异常，请联系客服"
			default:
				msg = "Token 不可用"
			}
			c.Header("X-MAS-Status", statusTextMA(maToken.Status))
			c.JSON(http.StatusForbidden, gin.H{"error": msg, "status": statusTextMA(maToken.Status)})
			c.Abort()
			return
		}

		// 并发检查
		if maToken.ConcurrencyLimit > 0 {
			maConcMu.Lock()
			current := maConcurrent[maToken.ID]
			if current >= maToken.ConcurrencyLimit {
				maConcMu.Unlock()
				c.Header("X-MAS-Status", "active")
				c.Header("X-MAS-Remaining", formatRemainingMA(maToken))
				c.Header("X-MAS-Tier", maToken.Tier)
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "并发路数已达上限"})
				c.Abort()
				return
			}
			maConcurrent[maToken.ID]++
			maConcMu.Unlock()
			defer func() {
				maConcMu.Lock()
				maConcurrent[maToken.ID]--
				maConcMu.Unlock()
			}()
		}

		// 模型权限检查（仅 chat/completions 端点）
		if c.Request.URL.Path == "/v1/chat/completions" {
			allowed := maToken.AllowedModels()
			reqModel := extractRequestedModelMA(c)
			if reqModel != "" && !containsModelMA(allowed, reqModel) {
				c.Header("X-MAS-Status", "active")
				c.Header("X-MAS-Remaining", formatRemainingMA(maToken))
				c.Header("X-MAS-Tier", maToken.Tier)
				c.JSON(http.StatusForbidden, gin.H{
					"error":           "当前套餐不支持此模型",
					"requested_model": reqModel,
					"allowed_models":  allowed,
				})
				c.Abort()
				return
			}
		}

		// 注入响应头
		c.Header("X-MAS-Status", "active")
		c.Header("X-MAS-Remaining", formatRemainingMA(maToken))
		c.Header("X-MAS-Tier", maToken.Tier)
		c.Header("X-MAS-Student-ID", maToken.StudentID)

		// 用量递增（请求成功后）
		defer func() {
			if c.Writer.Status() < 400 {
				model.DB.Model(&maToken).Updates(map[string]interface{}{
					"used":      maToken.Used + 1,
					"remaining": maToken.Remaining - 1,
				})
				maToken.Used++
				maToken.Remaining--
				maToken.CheckAndSetStatus()
				if maToken.Status != 1 {
					model.DB.Model(&maToken).Update("status", maToken.Status)
				}
				// 告警检查
				if maToken.HasAlert() {
					now := time.Now()
					if maToken.LastAlertAt == nil || now.Sub(*maToken.LastAlertAt) > 30*time.Minute {
						model.DB.Model(&maToken).Update("last_alert_at", now)
						go sendQuotaAlertMA(&maToken)
					}
				}
			}
		}()

		c.Next()
	}
}

func extractBearerTokenMA(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if len(auth) > 7 && strings.HasPrefix(auth, "Bearer ") {
		return auth[7:]
	}
	return ""
}

func extractRequestedModelMA(c *gin.Context) string {
	if c.Request.Body == nil || c.Request.ContentLength <= 0 {
		return ""
	}
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return ""
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	var req struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		return ""
	}
	return req.Model
}

func containsModelMA(models []string, target string) bool {
	for _, m := range models {
		if m == target {
			return true
		}
	}
	return false
}

func formatRemainingMA(m model.MASToken) string {
	if m.Quota < 0 {
		return "unlimited"
	}
	r := m.Remaining - 1
	if r < 0 {
		r = 0
	}
	return fmt.Sprintf("%d", r)
}

func statusTextMA(s int) string {
	switch s {
	case 1:
		return "active"
	case 2:
		return "exhausted"
	case 3:
		return "expired"
	case 4:
		return "banned"
	default:
		return "unknown"
	}
}

func sendQuotaAlertMA(m *model.MASToken) {
	if m.NotifyURL == "" {
		return
	}
	payload := map[string]interface{}{
		"event":           "quota_alert",
		"token":           m.Token,
		"student_id":      m.StudentID,
		"tier":            m.Tier,
		"remaining":       m.Remaining,
		"usage_percent":   int(float64(m.Used) / float64(m.Quota) * 100),
		"message":         fmt.Sprintf("您的%s套餐剩余 %d 次调用，建议及时续费", m.TierName, m.Remaining),
	}
	body, _ := json.Marshal(payload)
	http.Post(m.NotifyURL, "application/json", bytes.NewBuffer(body))
}