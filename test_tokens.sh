#!/bin/bash
DB="/home/admin/.openclaw/workspace/atmApi/data/atmapi.db"
API="http://localhost:3002/api/v1/chat/completions"
NOW=$(date +%s)

echo "=========================================="
echo "Token 计数准确性测试"
echo "时间: $(date '+%Y-%m-%d %H:%M:%S')"
echo "=========================================="
echo ""

# 从数据库读取完整 key
get_key() { sqlite3 $DB "SELECT key FROM tokens WHERE id=$1;"; }

KEY_1=$(get_key 1)
KEY_40=$(get_key 40)
KEY_45=$(get_key 45)
KEY_50=$(get_key 50)
KEY_96=$(get_key 96)
KEY_101=$(get_key 101)
KEY_106=$(get_key 106)

# ID|名称|Key|请求次数
declare -a TESTS=(
  "1|团队卡|$KEY_1|5"
  "40|性价比月卡|$KEY_40|5"
  "45|基础版月卡|$KEY_45|5"
  "50|升级版月卡|$KEY_50|5"
  "96|黄金月卡|$KEY_96|5"
  "101|大胃王月卡|$KEY_101|5"
  "106|9.9元体验版|$KEY_106|5"
)

for test in "${TESTS[@]}"; do
  IFS='|' read -r id name key count <<< "$test"
  
  echo "━━━ $name (ID: $id) ━━━"
  
  # 测试前状态
  before_quota=$(sqlite3 $DB "SELECT remain_quota FROM tokens WHERE id=$id;")
  before_rate=$(sqlite3 $DB "SELECT COUNT(*) FROM rate_limits WHERE token_id=$id AND request_time > $((NOW - 60));")
  
  # 发送请求
  success=0
  fail=0
  for i in $(seq 1 $count); do
    code=$(curl -s -o /dev/null -w "%{http_code}" -X POST $API \
      -H "Content-Type: application/json" \
      -H "Authorization: Bearer $key" \
      -d '{"model":"qwen3.5-plus","messages":[{"role":"user","content":"hi"}],"max_tokens":10}')
    if [ "$code" = "200" ]; then
      ((success++))
    else
      ((fail++))
    fi
  done
  
  # 测试后状态
  after_quota=$(sqlite3 $DB "SELECT remain_quota FROM tokens WHERE id=$id;")
  after_rate=$(sqlite3 $DB "SELECT COUNT(*) FROM rate_limits WHERE token_id=$id AND request_time > $((NOW - 60));")
  
  rate_diff=$((after_rate - before_rate))
  
  # 输出结果
  if [ "$success" = "$count" ]; then
    echo "  请求: ✅ $success/$count 成功"
  else
    echo "  请求: ❌ $success/$count 成功, $fail 失败"
  fi
  
  # 配额检查
  if [ "$id" = "106" ]; then
    quota_diff=$((before_quota - after_quota))
    if [ "$quota_diff" = "$count" ]; then
      echo "  配额: ✅ $before_quota → $after_quota (-$quota_diff)"
    else
      echo "  配额: ❌ $before_quota → $after_quota (期望 -$count, 实际 -$quota_diff)"
    fi
  else
    echo "  配额: $after_quota (月卡/团队卡不扣减)"
  fi
  
  # 滑动窗口检查
  if [ "$rate_diff" -ge "$count" ]; then
    echo "  限流: ✅ rate_limits +$rate_diff"
  elif [ "$rate_diff" -gt 0 ]; then
    echo "  限流: ⚠️  rate_limits +$rate_diff (期望 +$count)"
  else
    echo "  限流: — 无记录"
  fi
  
  echo ""
done

echo "=========================================="
echo "测试完成 $(date '+%H:%M:%S')"
echo "=========================================="
