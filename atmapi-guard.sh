#!/bin/bash
# atmApi SLA 监控 + 进程守护
# 每 2 分钟执行一次，由 crontab 调度
# 指标：健康检查响应时间、错误率、三节点存活

LOG="/tmp/atmapi-guard.log"
ALERT_FILE="/tmp/atmapi-alert.last"
ALERT_COOLDOWN=1800  # 30分钟内不重复告警
SLOW_THRESHOLD=3000   # 响应超过3秒告警

now=$(date '+%Y-%m-%d %H:%M:%S')

# ========== 1. 健康检查 ==========
start_ms=$(date +%s%3N)
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 http://localhost:3300/health 2>/dev/null)
end_ms=$(date +%s%3N)
latency=$((end_ms - start_ms))

if [ "$HTTP_CODE" != "200" ]; then
    echo "[$now] ❌ /health 返回 $HTTP_CODE，重启服务..." >> $LOG
    systemctl restart atmapi 2>/dev/null || systemctl --user restart atmapi 2>/dev/null
fi

# ========== 2. 延迟告警 ==========
if [ "$latency" -gt "$SLOW_THRESHOLD" ]; then
    last_alert=0
    [ -f "$ALERT_FILE" ] && last_alert=$(cat "$ALERT_FILE" 2>/dev/null)
    alert_now=$(date +%s)
    if [ $((alert_now - last_alert)) -gt $ALERT_COOLDOWN ]; then
        echo "[$now] ⚠️ 响应延迟 ${latency}ms 超过阈值 ${SLOW_THRESHOLD}ms" >> $LOG
        echo "$alert_now" > "$ALERT_FILE"
    fi
fi

# ========== 3. 错误率检查 ==========
ERRORS_1H=$(sqlite3 /home/admin/.openclaw/workspace/atmApi/data/atmapi.db \
    "SELECT COUNT(*) FROM request_logs WHERE created_at > datetime('now','-1 hour') AND status_code >= 400;" 2>/dev/null)
TOTAL_1H=$(sqlite3 /home/admin/.openclaw/workspace/atmApi/data/atmapi.db \
    "SELECT COUNT(*) FROM request_logs WHERE created_at > datetime('now','-1 hour');" 2>/dev/null)

if [ -n "$ERRORS_1H" ] && [ -n "$TOTAL_1H" ] && [ "$TOTAL_1H" -gt 0 ]; then
    ERROR_RATE=$((ERRORS_1H * 100 / TOTAL_1H))
    if [ "$ERROR_RATE" -gt 10 ]; then
        last_alert=0
        [ -f "$ALERT_FILE" ] && last_alert=$(cat "$ALERT_FILE" 2>/dev/null)
        alert_now=$(date +%s)
        if [ $((alert_now - last_alert)) -gt $ALERT_COOLDOWN ]; then
            echo "[$now] ⚠️ 1小时内错误率 ${ERROR_RATE}%（${ERRORS_1H}/${TOTAL_1H}）" >> $LOG
            echo "$alert_now" > "$ALERT_FILE"
        fi
    fi
fi

# ========== 4. 每日汇总（凌晨写入日志） ==========
HOUR=$(date +%H)
if [ "$HOUR" = "00" ] && [ ! -f "/tmp/atmapi-daily-summary-$(date +%Y%m%d)" ]; then
    YESTERDAY=$(date -d yesterday +%Y-%m-%d)
    YESTERDAY_TOTAL=$(sqlite3 /home/admin/.openclaw/workspace/atmApi/data/atmapi.db \
        "SELECT COUNT(*) FROM request_logs WHERE date(created_at)='$YESTERDAY';" 2>/dev/null)
    YESTERDAY_ERRORS=$(sqlite3 /home/admin/.openclaw/workspace/atmApi/data/atmapi.db \
        "SELECT COUNT(*) FROM request_logs WHERE date(created_at)='$YESTERDAY' AND status_code >= 400;" 2>/dev/null)
    YESTERDAY_AVG=$(sqlite3 /home/admin/.openclaw/workspace/atmApi/data/atmapi.db \
        "SELECT ROUND(AVG(duration_ms)) FROM request_logs WHERE date(created_at)='$YESTERDAY';" 2>/dev/null)
    echo "[$now] 📊 昨日汇总：请求${YESTERDAY_TOTAL}次，错误${YESTERDAY_ERRORS}次，平均延迟${YESTERDAY_AVG}ms" >> $LOG
    touch "/tmp/atmapi-daily-summary-$(date +%Y%m%d)"
fi
