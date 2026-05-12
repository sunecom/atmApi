#!/bin/bash
# atmApi 外部监控告警脚本

URL="http://8.220.139.36:3002/health"
ALERT_THRESHOLD=3  # 连续失败 3 次才告警
FAIL_COUNT=0

# 检查历史失败次数
if [ -f /tmp/atmapi_fail_count ]; then
    FAIL_COUNT=$(cat /tmp/atmapi_fail_count)
fi

# 检查服务状态
STATUS=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 $URL)

if [ "$STATUS" != "200" ]; then
    FAIL_COUNT=$((FAIL_COUNT + 1))
    echo $FAIL_COUNT > /tmp/atmapi_fail_count
    
    if [ $FAIL_COUNT -ge $ALERT_THRESHOLD ]; then
        echo "[$(date)] ❌ atmApi 服务异常！连续失败 $FAIL_COUNT 次"
        # 发送告警（可配置邮件/企微/飞书/短信）
        echo "atmApi 服务异常，请及时处理！" | mail -s "atmApi 告警" admin@example.com 2>/dev/null
    fi
else
    # 服务正常，重置计数
    echo "0" > /tmp/atmapi_fail_count
fi
