#!/bin/bash
# atmApi 增强型健康检查脚本
# 用法：bash healthcheck-enhanced.sh [--notify]

PORT=3002
APP_DIR="/home/admin/.openclaw/workspace/atmApi"
LOG_FILE="$APP_DIR/data/healthcheck.log"
ALERT_FLAG="$APP_DIR/data/.alert_sent"
NOTIFY=${2:-false}

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" >> "$LOG_FILE"
}

# 发送飞书告警
send_alert() {
    local message="$1"
    if [ "$NOTIFY" = "--notify" ]; then
        # 避免重复告警（5分钟内只发一次）
        if [ -f "$ALERT_FLAG" ]; then
            LAST_ALERT=$(stat -c%Y "$ALERT_FLAG" 2>/dev/null || stat -f%m "$ALERT_FLAG" 2>/dev/null)
            NOW=$(date +%s)
            if [ $((NOW - LAST_ALERT)) -lt 300 ]; then
                return 0
            fi
        fi
        
        # 这里可以替换为实际的飞书 webhook
        # curl -X POST "https://open.feishu.cn/open-apis/bot/v2/hook/YOUR_HOOK" \
        #   -H "Content-Type: application/json" \
        #   -d "{\"msg_type\":\"text\",\"content\":{\"text\":\"atmApi告警: $message\"}}"
        
        touch "$ALERT_FLAG"
        log "📢 已发送告警: $message"
    fi
}

# 检查1: 端口监听
echo "=== 检查1: 端口监听 ==="
if ss -tlnp | grep -q ":$PORT "; then
    log "✅ 端口 $PORT 正常监听"
else
    log "❌ 端口 $PORT 未监听"
    send_alert "端口 $PORT 未监听，服务可能已崩溃"
    
    # 尝试重启
    sudo systemctl restart atmapi
    sleep 5
    if ss -tlnp | grep -q ":$PORT "; then
        log "✅ 重启成功"
    else
        log "❌ 重启失败"
        send_alert "重启失败，需人工介入"
    fi
    exit 1
fi

# 检查2: HTTP 响应
echo "=== 检查2: HTTP 响应 ==="
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 http://localhost:$PORT/)
if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "302" ]; then
    log "✅ HTTP 响应正常 (HTTP $HTTP_CODE)"
else
    log "⚠️ HTTP 响应异常 (HTTP $HTTP_CODE)"
    send_alert "HTTP 响应异常: $HTTP_CODE"
fi

# 检查3: systemd 状态
echo "=== 检查3: systemd 状态 ==="
SYSTEMD_STATUS=$(systemctl is-active atmapi 2>/dev/null)
if [ "$SYSTEMD_STATUS" = "active" ]; then
    log "✅ systemd 状态: active"
else
    log "❌ systemd 状态: $SYSTEMD_STATUS"
    send_alert "systemd 状态异常: $SYSTEMD_STATUS"
fi

# 检查4: 磁盘空间
echo "=== 检查4: 磁盘空间 ==="
DISK_USAGE=$(df -h / | tail -1 | awk '{print $5}' | sed 's/%//')
if [ "$DISK_USAGE" -gt 90 ]; then
    log "⚠️ 磁盘使用率过高: ${DISK_USAGE}%"
    send_alert "磁盘使用率过高: ${DISK_USAGE}%"
elif [ "$DISK_USAGE" -gt 80 ]; then
    log "⚠️ 磁盘使用率较高: ${DISK_USAGE}%"
else
    log "✅ 磁盘使用率正常: ${DISK_USAGE}%"
fi

# 检查5: 内存使用
echo "=== 检查5: 内存使用 ==="
MEM_USAGE=$(ps -p $(cat $APP_DIR/data/atmapi.pid 2>/dev/null) -o %mem= 2>/dev/null | tr -d ' ')
if [ -n "$MEM_USAGE" ]; then
    if [ "${MEM_USAGE%.*}" -gt 80 ]; then
        log "⚠️ 内存使用率过高: ${MEM_USAGE}%"
        send_alert "atmApi 内存使用率过高: ${MEM_USAGE}%"
    else
        log "✅ 内存使用率正常: ${MEM_USAGE}%"
    fi
else
    log "⚠️ 无法获取内存使用率"
fi

# 检查6: 最近错误日志
echo "=== 检查6: 最近错误日志 ==="
ERROR_COUNT=$(tail -100 "$APP_DIR/data/atmapi-systemd-error.log" 2>/dev/null | grep -ci "error\|panic\|fatal" || echo "0")
ERROR_COUNT=$(echo "$ERROR_COUNT" | tr -d '[:space:]')
if [ -z "$ERROR_COUNT" ]; then ERROR_COUNT=0; fi
if [ "$ERROR_COUNT" -gt 10 ]; then
    log "⚠️ 最近有 $ERROR_COUNT 条错误日志"
    send_alert "最近有 $ERROR_COUNT 条错误日志"
else
    log "✅ 错误日志数量正常: $ERROR_COUNT"
fi

echo ""
echo "健康检查完成，详见 $LOG_FILE"
