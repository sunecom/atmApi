#!/bin/bash
# atmApi 健康检查监控脚本

URL="http://localhost:3002/health"
ALERT_URL="http://8.220.139.36:3002/health"

echo "=== atmApi 健康检查 ==="
echo "时间：$(date '+%Y-%m-%d %H:%M:%S')"
echo ""

# 本地检查
LOCAL_STATUS=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 $URL)
if [ "$LOCAL_STATUS" = "200" ]; then
    echo "✅ 本地服务正常"
else
    echo "❌ 本地服务异常（HTTP $LOCAL_STATUS）"
fi

# 公网检查
PUBLIC_STATUS=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 $ALERT_URL)
if [ "$PUBLIC_STATUS" = "200" ]; then
    echo "✅ 公网访问正常"
else
    echo "❌ 公网访问异常（HTTP $PUBLIC_STATUS）"
fi

echo ""
echo "=== 服务信息 ==="
curl -s $URL | python3 -c "import sys,json; d=json.load(sys.stdin); print(f'版本: {d[\"version\"]}\n时间: {d[\"time\"]}')"

echo ""
echo "=== 数据统计 ==="
TOKEN=$(curl -s -X POST http://localhost:3002/api/v1/login -H "Content-Type: application/json" -d '{"username":"admin","password":"admin123"}' | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
echo "渠道：$(curl -s http://localhost:3002/api/v1/channels -H "Authorization: Bearer $TOKEN" | python3 -c "import sys,json; print(len(json.load(sys.stdin).get('data',[])))")"
echo "Token：$(curl -s http://localhost:3002/api/v1/tokens -H "Authorization: Bearer $TOKEN" | python3 -c "import sys,json; print(len(json.load(sys.stdin).get('data',[])))")"
echo "用户：$(curl -s http://localhost:3002/api/v1/users -H "Authorization: Bearer $TOKEN" | python3 -c "import sys,json; print(len(json.load(sys.stdin).get('data',[])))")"
echo "日志：$(curl -s http://localhost:3002/api/v1/logs -H "Authorization: Bearer $TOKEN" | python3 -c "import sys,json; print(len(json.load(sys.stdin).get('data',[])))")"
