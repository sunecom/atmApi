#!/bin/bash
# atmApi 功能测试脚本
# 用法：bash test.sh

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TOKEN_FILE="$HOME/.openclaw/workspace/.local_tokens.json"

if [ ! -f "$TOKEN_FILE" ]; then
    echo "❌ token 文件不存在，请先执行 python3 保存 token"
    exit 1
fi

# 从 JSON 文件读取 token（用 python 避免 bash 脱敏）
API_KEY=$(python3 -c "import json; print(json.load(open('$TOKEN_FILE'))['api_keys']['atm_team'])")
JWT=$(python3 -c "import json; print(json.load(open('$TOKEN_FILE'))['jwt_tokens']['admin'])")

BASE="http://localhost:3002"
PASS=0
FAIL=0

check() {
    local name="$1"
    local status="$2"
    if [ "$status" = "ok" ]; then
        echo "  ✅ $name"
        PASS=$((PASS+1))
    else
        echo "  ❌ $name"
        FAIL=$((FAIL+1))
    fi
}

echo "=============================="
echo "  atmApi 全面测试 $(date +%H:%M:%S)"
echo "=============================="
echo ""

# 1. 健康检查
echo "[1] 基础功能"
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/health")
check "健康检查" "$([ "$STATUS" = "200" ] && echo "ok" || echo "fail")"

# 2. 首页
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/")
check "首页" "$([ "$STATUS" = "200" ] && echo "ok" || echo "fail")"

# 3. monitor 页面
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/static/monitor.html")
check "monitor.html" "$([ "$STATUS" = "200" ] && echo "ok" || echo "fail")"

# 4. token-info 页面
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/static/token-info.html")
check "token-info.html" "$([ "$STATUS" = "200" ] && echo "ok" || echo "fail")"

# 5. /token-info 重定向
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/token-info")
check "/token-info 重定向" "$([ "$STATUS" = "200" ] && echo "ok" || echo "fail")"

echo ""

# 6. 智能路由：简单问题
echo "[2] 智能路由"
MODEL=$(curl -s -X POST "$BASE/api/v1/chat/completions" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"glm-5.2","messages":[{"role":"user","content":"你好"}]}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin).get('model',''))")
check "简单问题 → glm-4.7 (实际: $MODEL)" "$([ "$MODEL" = "glm-4.7" ] && echo "ok" || echo "fail")"

MODEL=$(curl -s -X POST "$BASE/api/v1/chat/completions" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"glm-5.2","messages":[{"role":"user","content":"介绍一下 Python 和 Java 的区别"}]}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin).get('model',''))")
check "中等问题 → deepseek (实际: $MODEL)" "$([ "$MODEL" = "deepseek-v4-flash" ] && echo "ok" || echo "fail")"

MODEL=$(curl -s -X POST "$BASE/api/v1/chat/completions" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"glm-5.2","messages":[{"role":"user","content":"详细分析一下分布式系统的一致性和可用性之间的权衡"}]}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin).get('model',''))")
check "复杂问题 → glm-5.2 (实际: $MODEL)" "$([ "$MODEL" = "glm-5.2" ] && echo "ok" || echo "fail")"

echo ""

# 7. 缓存功能
echo "[3] 缓存功能"
# 清空前置缓存统计
STATS_BEFORE=$(curl -s "$BASE/cache/stats" | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['total_hits'])")

# 第一次请求
curl -s -X POST "$BASE/api/v1/chat/completions" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"glm-5.2","messages":[{"role":"user","content":"test cache function"}]}' > /dev/null
sleep 1

# 第二次请求（应命中缓存）
curl -s -X POST "$BASE/api/v1/chat/completions" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"glm-5.2","messages":[{"role":"user","content":"test cache function"}]}' > /dev/null

# 检查缓存命中次数
STATS_AFTER=$(curl -s "$BASE/cache/stats" | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['total_hits'])")
check "缓存命中 (before=$STATS_BEFORE, after=$STATS_AFTER)" "$([ "$STATS_AFTER" -gt "$STATS_BEFORE" ] && echo "ok" || echo "fail")"

echo ""

# 8. 成本分析 API
echo "[4] 成本分析 API"
STATUS=$(curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $JWT" "$BASE/api/v1/cost-summary")
check "cost-summary" "$([ "$STATUS" = "200" ] && echo "ok" || echo "fail")"

STATUS=$(curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $JWT" "$BASE/api/v1/cost-by-plan")
check "cost-by-plan" "$([ "$STATUS" = "200" ] && echo "ok" || echo "fail")"

STATUS=$(curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $JWT" "$BASE/api/v1/cost-trend")
check "cost-trend" "$([ "$STATUS" = "200" ] && echo "ok" || echo "fail")"

echo ""

# 9. 缓存统计
echo "[5] 缓存统计"
CACHE_SIZE=$(curl -s "$BASE/cache/stats" | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['size'])")
check "缓存统计 (size=$CACHE_SIZE)" "$([ -n "$CACHE_SIZE" ] && echo "ok" || echo "fail")"

echo ""
echo "=============================="
echo "  结果: ✅ $PASS 通过 / ❌ $FAIL 失败"
echo "=============================="

exit $FAIL
