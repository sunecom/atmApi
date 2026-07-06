#!/bin/bash
APP_DIR="/home/admin/.openclaw/workspace/atmApi"
cd "$APP_DIR" || exit 1

# 加载支付宝环境变量
source <(python3 -c "
import os
with open('.env.alipay') as f:
    for line in f:
        line = line.strip()
        if not line or line.startswith('#'):
            continue
        if '=' in line:
            key, val = line.split('=', 1)
            # 去引号
            val = val.strip('\'"')
            print(f'export {key}={repr(val)}')
")

# kill 旧进程
for pid in $(pgrep -f "atmapi$"); do
    echo "kill $pid"
    kill "$pid" 2>/dev/null
done
sleep 2

# 启动新进程
nohup ./atmapi > atmapi.log 2>&1 &
echo "atmApi PID: $!"
sleep 2
echo "Active: $(pgrep -f 'atmapi$' | wc -l)"
