#!/bin/bash
# 飞书 Webhook 配置文件
# 请替换为实际的飞书机器人 webhook URL

FEISHU_WEBHOOK_URL="https://open.feishu.cn/open-apis/bot/v2/hook/YOUR_HOOK_TOKEN_HERE"

# 告警函数
send_feishu_alert() {
    local message="$1"
    if [ -n "$FEISHU_WEBHOOK_URL" ] && [ "$FEISHU_WEBHOOK_URL" != "https://open.feishu.cn/open-apis/bot/v2/hook/YOUR_HOOK_TOKEN_HERE" ]; then
        curl -s -X POST "$FEISHU_WEBHOOK_URL" \
          -H "Content-Type: application/json" \
          -d "{\"msg_type\":\"text\",\"content\":{\"text\":\"🚨 atmApi 告警: $message\"}}" > /dev/null 2>&1
    fi
}
