#!/bin/bash
# atmApi 健康检查脚本
# 用法：bash healthcheck.sh

PORT=3002
LOG_FILE="/home/admin/.openclaw/workspace/atmApi/logs/healthcheck.log"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" >> "$LOG_FILE"
}

# 检查端口是否监听
if ss -tlnp | grep -q ":$PORT "; then
    log "✅ 端口 $PORT 正常监听"
else
    log "❌ 端口 $PORT 未监听，尝试重启..."
    cd /home/admin/.openclaw/workspace/atmApi
    pkill -f atmapi
    sleep 2
    nohup ./atmapi > logs/atmapi.log 2>&1 &
    log "🔄 atmApi 已重启"
fi

# 检查 API 响应
RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 http://localhost:$PORT/api/stats)
if [ "$RESPONSE" = "200" ]; then
    log "✅ API 响应正常 (HTTP $RESPONSE)"
else
    log "⚠️ API 响应异常 (HTTP $RESPONSE)"
fi

# 检查磁盘空间
DISK_USAGE=$(df -h / | tail -1 | awk '{print $5}' | sed 's/%//')
if [ "$DISK_USAGE" -gt 90 ]; then
    log "⚠️ 磁盘使用率过高: ${DISK_USAGE}%"
else
    log "✅ 磁盘使用率正常: ${DISK_USAGE}%"
fi

echo "健康检查完成，详见 $LOG_FILE"
