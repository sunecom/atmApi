package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// FeishuNotifier 飞书通知器
type FeishuNotifier struct {
	appID     string
	appSecret string
	accessToken string
	tokenExpiry time.Time
	mu        sync.Mutex
}

// GlobalFeishuNotifier 全局飞书通知器实例
var GlobalFeishuNotifier *FeishuNotifier

// InitFeishuNotifier 初始化全局飞书通知器
func InitFeishuNotifier(appID, appSecret string) {
	GlobalFeishuNotifier = NewFeishuNotifier(appID, appSecret)
}

// NewFeishuNotifier 创建飞书通知器
func NewFeishuNotifier(appID, appSecret string) *FeishuNotifier {
	return &FeishuNotifier{
		appID:     appID,
		appSecret: appSecret,
	}
}

// getAccessToken 获取飞书 access token（带缓存）
func (f *FeishuNotifier) getAccessToken() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// 如果 token 还有效（提前 5 分钟刷新）
	if f.accessToken != "" && time.Now().Before(f.tokenExpiry.Add(-5*time.Minute)) {
		return f.accessToken, nil
	}

	// 请求新 token
	url := "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal"
	payload := map[string]string{
		"app_id":     f.appID,
		"app_secret": f.appSecret,
	}
	payloadBytes, _ := json.Marshal(payload)

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return "", fmt.Errorf("请求飞书 token 失败: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("解析飞书 token 响应失败: %w", err)
	}

	if code, ok := result["code"].(float64); ok && code != 0 {
		return "", fmt.Errorf("飞书 token 获取失败: code=%v, msg=%v", code, result["msg"])
	}

	token, ok := result["tenant_access_token"].(string)
	if !ok {
		return "", fmt.Errorf("飞书 token 响应格式错误")
	}

	expire, _ := result["expire"].(float64)
	f.accessToken = token
	f.tokenExpiry = time.Now().Add(time.Duration(expire) * time.Second)

	log.Printf("[飞书通知] access token 已刷新，有效期 %.0f 秒", expire)
	return token, nil
}

// SendTextMessage 发送文本消息给用户
func (f *FeishuNotifier) SendTextMessage(openID, text string) error {
	token, err := f.getAccessToken()
	if err != nil {
		return err
	}

	url := "https://open.feishu.cn/open-apis/im/v1/messages"
	payload := map[string]interface{}{
		"receive_id": openID,
		"msg_type":   "text",
		"content":    fmt.Sprintf(`{"text":%q}`, text),
	}
	payloadBytes, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", url+"?receive_id_type=open_id", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("发送消息失败: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	if code, ok := result["code"].(float64); ok && code != 0 {
		return fmt.Errorf("飞书 API 错误: code=%v, msg=%v", code, result["msg"])
	}

	log.Printf("[飞书通知] 消息已发送给 %s", openID)
	return nil
}

// SendCostAlert 发送成本告警
func (f *FeishuNotifier) SendCostAlert(tokenName, planName string, cost, revenue, profit, profitMargin float64) error {
	text := fmt.Sprintf(`⚠️ 成本告警

Token: %s
套餐: %s
本月成本: ¥%.2f
本月收入: ¥%.2f
亏损: ¥%.2f
利润率: %.1f%%

建议：检查该 token 的使用情况，考虑调整套餐或限制用量。`,
		tokenName, planName, cost, revenue, -profit, profitMargin)

	// 建国的 open_id
	openID := "ou_b0a92259af15573ff686e29ac12cd463"
	return f.SendTextMessage(openID, text)
}

// SendExpiryAlert 发送过期告警
func (f *FeishuNotifier) SendExpiryAlert(tokenName string, daysLeft int) error {
	var text string
	if daysLeft <= 0 {
		text = fmt.Sprintf(`🚨 Token 已过期

Token: %s
状态: 已自动禁用

该 token 已过期并被自动禁用。如需继续使用，请联系管理员续费。`, tokenName)
	} else {
		text = fmt.Sprintf(`⚠️ Token 即将过期

Token: %s
剩余天数: %d 天

请尽快续费，避免服务中断。`, tokenName, daysLeft)
	}

	openID := "ou_b0a92259af15573ff686e29ac12cd463"
	return f.SendTextMessage(openID, text)
}

// SendUsageAlert 发送用量告警
func (f *FeishuNotifier) SendUsageAlert(tokenName, quotaType string, used, limit int64, percentage int) error {
	var emoji string
	if percentage >= 100 {
		emoji = "🚨"
	} else if percentage >= 90 {
		emoji = "⚠️"
	} else {
		emoji = "📊"
	}

	text := fmt.Sprintf(`%s 用量告警

Token: %s
配额类型: %s
已使用: %d / %d
使用率: %d%%

%s，请注意控制用量。`, emoji, tokenName, quotaType, used, limit, percentage,
		func() string {
			if percentage >= 100 {
				return "配额已用完"
			} else if percentage >= 90 {
				return "配额即将用完"
			}
			return "配额使用率较高"
		}())

	openID := "ou_b0a92259af15573ff686e29ac12cd463"
	return f.SendTextMessage(openID, text)
}
