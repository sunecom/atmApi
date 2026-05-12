#!/bin/bash
# atmApi 守护脚本
# 检查 atmApi 服务是否运行，如果崩溃则自动重启

APP_DIR="/home/admin/.openclaw/workspace/atmApi"
APP_BIN="$APP_DIR/atmapi"
LOG_FILE="$APP_DIR/data/atmapi-guard.log"
PORT=3002
PID_FILE="$APP_DIR/data/atmapi.pid"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" >> "$LOG_FILE"
}

# 检查进程是否运行
check_process() {
    if [ -f "$PID_FILE" ]; then
        PID=$(cat "$PID_FILE")
        if kill -0 "$PID" 2>/dev/null; then
            return 0  # 进程运行中
        fi
    fi
    return 1  # 进程未运行
}

# 启动服务
start_service() {
    log "启动 atmApi 服务..."
    cd "$APP_DIR"
    PORT=$PORT nohup "$APP_BIN" > "$APP_DIR/data/atmapi.log" 2>&1 &
    PID=$!
    echo $PID > "$PID_FILE"
    log "atmApi 已启动，PID: $PID"
}

# 主循环
while true; do
    if ! check_process; then
        log "atmApi 服务未运行，尝试重启..."
        start_service
        sleep 2
        # 验证是否启动成功
        if check_process; then
            log "重启成功"
        else
            log "重启失败，等待 30 秒后重试..."
            sleep 30
        fi
    fi
    sleep 30
done
