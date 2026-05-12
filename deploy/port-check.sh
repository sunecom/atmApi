#!/bin/bash
PORT=3002
PID=$(lsof -t -i:$PORT 2>/dev/null)
if [ -n "$PID" ] && ! ps -p $PID > /dev/null 2>&1; then
    kill -9 $PID 2>/dev/null
    sleep 1
    systemctl restart atmapi 2>/dev/null
fi
