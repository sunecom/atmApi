#!/bin/bash
# 重启 atmApi 并加载支付宝配置
APP_DIR="/home/admin/.openclaw/workspace/atmApi"
cd "$APP_DIR"

# 先加载支付宝环境变量
export $(grep -v '^#' .env.alipay | xargs -d '\n')

# kill 旧进程
PID=$(pgrep -f "atmapi$")
if [ -n "$PID" ]; then
    kill -15 $PID 2>/dev/null
    sleep 2
    # 如果还在运行，强制 kill
    PID2=$(pgrep -f "atmapi$")
    if [ -n "$PID2" ]; then
        kill -9 $PID2 2>/dev/null
        sleep 1
    fi
fi

# 启动新进程（确保环境变量生效）
nohup ./atmapi > atmapi.log 2>&1 &
sleep 2
NEWPID=$(pgrep -f "atmapi$")
echo "atmApi restarted, PID: $NEWPID"
