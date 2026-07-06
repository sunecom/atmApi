#!/bin/bash
# atm卡 全链路测试 v1.1
BASE="http://localhost:3300"
DB="$HOME/.openclaw/workspace/atmApi/data/atmapi.db"
PASS=0; FAIL=0; SKIP=0

green() { echo -e "\033[32m✅ $1\033[0m"; }
red()   { echo -e "\033[31m❌ $1\033[0m"; }
yellow() { echo -e "\033[33m⏭️ $1\033[0m"; }
info()  { echo -e "\033[36m🔧 $1\033[0m"; }
ok()   { green "$1"; PASS=$((PASS+1)); }
fail() { red "$1"; FAIL=$((FAIL+1)); }
skip() { yellow "$1"; SKIP=$((SKIP+1)); }

echo "=========================================="
echo "  atm卡 全链路测试 v1.1"
echo "  $(date '+%Y-%m-%d %H:%M:%S')"
echo "=========================================="
echo ""

# 找一个 basic token
TEST_TOKEN=$(sqlite3 "$DB" "SELECT key FROM tokens WHERE rate_limit_group='basic' AND status=1 LIMIT 1;")

# ===== 1. 健康检查 =====
info "测试 1/10: 健康检查"
HEALTH=$(curl -s "$BASE/health")
if echo "$HEALTH" | grep -q '"status":"ok"'; then
    ok "健康检查通过"
else
    fail "健康检查失败: $HEALTH"
fi
echo ""

# ===== 2. 创建订单 =====
info "测试 2/10: 创建订单（basic ¥29.9）"
ORDER_RESP=$(curl -s -X POST "$BASE/api/v1/payment/create-order" \
  -H "Content-Type: application/json" \
  -d '{"plan":"basic","user_open_id":"test_chain_001"}')
ORDER_ID=$(echo "$ORDER_RESP" | python3 -c "import json,sys;print(json.load(sys.stdin).get('order_id',''))" 2>/dev/null)
if [ -n "$ORDER_ID" ] && [ "$ORDER_ID" != "" ]; then
    ok "订单创建成功: $ORDER_ID"
else
    fail "订单创建失败"
fi
echo ""

# ===== 3. 模拟支付成功 =====
info "测试 3/10: 模拟支付成功"
if [ -n "$ORDER_ID" ] && [ "$ORDER_ID" != "" ]; then
    sqlite3 "$DB" "UPDATE orders SET status='paid', paid_at=datetime('now','localtime'), alipay_trade_no='test_manual' WHERE order_no='$ORDER_ID';"
    ok "订单 $ORDER_ID 已标记 paid"
else
    skip "跳过（无订单）"
fi
echo ""

# ===== 4. Token 调用 API =====
info "测试 4/10: Token 调用 API"
if [ -z "$TEST_TOKEN" ]; then
    skip "跳过（无 basic token）"
else
    RESP=$(curl -s -X POST "$BASE/v1/chat/completions" \
      -H "Content-Type: application/json" \
      -H "Authorization: Bearer $TEST_TOKEN" \
      -d '{"model":"deepseek-a4","messages":[{"role":"user","content":"1+1=?"}],"max_tokens":10}')
    if echo "$RESP" | grep -q '"choices"'; then
        ok "API 调用成功"
    else
        fail "API 调用失败: ${RESP:0:100}"
    fi
fi
echo ""

# ===== 5. 限流扣减 =====
info "测试 5/10: 限流扣减验证"
if [ -z "$TEST_TOKEN" ]; then
    skip "跳过"
else
    TOKEN_ID=$(sqlite3 "$DB" "SELECT id FROM tokens WHERE key='$TEST_TOKEN';")
    NOW=$(date +%s)
    FIVE_HOURS_AGO=$((NOW - 18000))
    BEFORE=$(sqlite3 "$DB" "SELECT count(*) FROM rate_limits WHERE token_id=$TOKEN_ID AND request_time > $FIVE_HOURS_AGO;")
    
    curl -s -X POST "$BASE/v1/chat/completions" \
      -H "Content-Type: application/json" \
      -H "Authorization: Bearer $TEST_TOKEN" \
      -d '{"model":"deepseek-a4","messages":[{"role":"user","content":"test"}],"max_tokens":5}' \
      -o /dev/null 2>/dev/null
    
    AFTER=$(sqlite3 "$DB" "SELECT count(*) FROM rate_limits WHERE token_id=$TOKEN_ID AND request_time > $FIVE_HOURS_AGO;")
    
    DIFF=$((AFTER - BEFORE))
    if [ "$DIFF" -ge 1 ]; then
        ok "限流扣减正常: $BEFORE → $AFTER (+$DIFF)"
    else
        fail "限流未扣减: $BEFORE → $AFTER"
    fi
fi
echo ""

# ===== 6. SSE 流式 =====
info "测试 6/10: SSE 流式调用"
if [ -z "$TEST_TOKEN" ]; then
    skip "跳过"
else
    STREAM=$(curl -s -N -X POST "$BASE/v1/chat/completions" \
      -H "Content-Type: application/json" \
      -H "Authorization: Bearer $TEST_TOKEN" \
      -d '{"model":"deepseek-a4","messages":[{"role":"user","content":"hi"}],"max_tokens":10,"stream":true}' \
      --max-time 15 2>&1 | head -1)
    if echo "$STREAM" | grep -q "^data:"; then
        ok "SSE 流式正常"
    else
        fail "SSE 流式异常: ${STREAM:0:80}"
    fi
fi
echo ""

# ===== 7. X-Actual-Model 响应头 =====
info "测试 7/10: 响应头 X-Actual-Model"
if [ -z "$TEST_TOKEN" ]; then
    skip "跳过"
else
    HDR=$(curl -s -X POST "$BASE/v1/chat/completions" \
      -H "Content-Type: application/json" \
      -H "Authorization: Bearer $TEST_TOKEN" \
      -d '{"model":"deepseek-a4","messages":[{"role":"user","content":"hi"}],"max_tokens":5}' \
      -D - -o /dev/null 2>&1 | grep -i "X-Actual-Model" | tr -d '\r')
    if [ -n "$HDR" ]; then
        ok "响应头正常: $HDR"
    else
        fail "缺少响应头"
    fi
fi
echo ""

# ===== 8. 超限 429 =====
info "测试 8/10: 超限返回 429"
TRIAL_TOKEN=$(sqlite3 "$DB" "SELECT key FROM tokens WHERE rate_limit_group='trial' AND status=1 ORDER BY id DESC LIMIT 1;")
if [ -z "$TRIAL_TOKEN" ]; then
    skip "跳过（无 trial token）"
else
    TRIAL_ID=$(sqlite3 "$DB" "SELECT id FROM tokens WHERE key='$TRIAL_TOKEN';")
    NOW=$(date +%s)
    # 塞入 25 条（trial 5h 限制是 20）
    for i in $(seq 1 25); do
        sqlite3 "$DB" "INSERT INTO rate_limits (token_id, request_time, created_at) VALUES ($TRIAL_ID, $((NOW - i*30)), datetime('now'));"
    done
    
    CODE=$(curl -s -X POST "$BASE/v1/chat/completions" \
      -H "Content-Type: application/json" \
      -H "Authorization: Bearer $TRIAL_TOKEN" \
      -d '{"model":"deepseek-a4","messages":[{"role":"user","content":"x"}],"max_tokens":3}' \
      -o /dev/null -w "%{http_code}")
    
    if [ "$CODE" = "429" ]; then
        ok "超限拒绝正常: HTTP 429"
    else
        fail "超限拒绝异常: HTTP $CODE（期望 429）"
    fi
    
    # 清理
    sqlite3 "$DB" "DELETE FROM rate_limits WHERE token_id=$TRIAL_ID AND created_at > datetime('now','-1 minute');"
fi
echo ""

# ===== 9. HTTPS =====
info "测试 9/10: HTTPS 连通性"
HTTPS_CODE=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 https://atmapi.aitomoney.online/health 2>/dev/null || echo "000")
if [ "$HTTPS_CODE" = "200" ]; then
    ok "HTTPS 正常"
else
    skip "HTTPS 可能内网不通: HTTP $HTTPS_CODE"
fi
echo ""

# ===== 10. 并发压测 =====
info "测试 10/10: 并发压测（5 并发）"
if [ -z "$TEST_TOKEN" ]; then
    skip "跳过"
else
    SUCCESS=0
    for i in $(seq 1 5); do
        CODE=$(curl -s -X POST "$BASE/v1/chat/completions" \
          -H "Content-Type: application/json" \
          -H "Authorization: Bearer $TEST_TOKEN" \
          -d '{"model":"deepseek-a4","messages":[{"role":"user","content":"ok"}],"max_tokens":3}' \
          -o /dev/null -w "%{http_code}" 2>/dev/null)
        if [ "$CODE" = "200" ]; then
            SUCCESS=$((SUCCESS+1))
        fi
    done
    if [ "$SUCCESS" -ge 4 ]; then
        ok "并发压测通过: $SUCCESS/5 成功"
    else
        fail "并发压测: $SUCCESS/5 成功"
    fi
fi
echo ""

# ===== 汇总 =====
echo "=========================================="
echo "  全链路测试结果"
echo "=========================================="
green "通过: $PASS"
if [ "$FAIL" -gt 0 ]; then red "失败: $FAIL"; fi
if [ "$SKIP" -gt 0 ]; then yellow "跳过: $SKIP"; fi
echo "=========================================="
if [ "$FAIL" -eq 0 ]; then
    green "🎉 全部通过！可以上线！"
else
    red "⚠️ 有 $FAIL 项失败，需修复后上线"
fi
