package service

import (
	"fmt"
	"log"
	"os"
)

// FeishuAlertCard 飞书告警卡片
type FeishuAlertCard struct {
	MsgType string `json:"msg_type"`
	Card    struct {
		Header struct {
			Title struct {
				Tag     string `json:"tag"`
				Content string `json:"content"`
			} `json:"title"`
			Template string `json:"template"`
		} `json:"header"`
		Elements []struct {
			Tag  string `json:"tag"`
			Text struct {
				Tag     string `json:"tag"`
				Content string `json:"content"`
			} `json:"text"`
		} `json:"elements"`
	} `json:"card"`
}

// SendFeishuAlert 已废弃，改用文件标记方案

// CheckAndAlertDeepSeekCost 检查 DeepSeek 消费并告警
// 写入标记文件，由 Gateway Cron 检测并入当前会话
func CheckAndAlertDeepSeekCost(channelName string, currentCost float64, dailyLimit float64) {
	if dailyLimit <= 0 {
		return // 无限制
	}

	// 如果已达到限额的 90%
	if currentCost >= dailyLimit*0.9 {
		alertFile := "/tmp/atmapi-deepseek-alert"
		msg := fmt.Sprintf("⚠️ **DeepSeek 消费告警**\n渠道: %s\n当前: %.2f / %.2f 元\n\n💡 **如需立即暂停 DeepSeek，请在 QQ 回复：暂停 DeepSeek**", 
			channelName, currentCost, dailyLimit)
		
		os.WriteFile(alertFile, []byte(msg), 0644)
		log.Printf("[告警] 已写入告警文件: %s", alertFile)
	}
}
