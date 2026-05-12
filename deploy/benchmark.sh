#!/bin/bash
# atmApi 性能压测脚本

URL="http://localhost:3002"
TOKEN=$(curl -s -X POST $URL/api/v1/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")

echo "=== atmApi 性能压测 ==="
echo "目标：$URL"
echo ""

# 健康检查压测
echo "1. 健康检查 (/health) - 100 次请求"
time for i in {1..100}; do curl -s -o /dev/null $URL/health; done
echo ""

# 登录压测
echo "2. 登录接口 (/api/v1/login) - 50 次请求"
time for i in {1..50}; do curl -s -o /dev/null -X POST $URL/api/v1/login -H "Content-Type: application/json" -d '{"username":"admin","password":"admin123"}'; done
echo ""

# 渠道列表压测
echo "3. 渠道列表 (/api/v1/channels) - 100 次请求"
time for i in {1..100}; do curl -s -o /dev/null $URL/api/v1/channels -H "Authorization: Bearer $TOKEN"; done
echo ""

echo "=== 压测完成 ==="
