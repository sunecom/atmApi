#!/bin/bash
# atmApi 数据库每日备份脚本

DB_PATH="/home/admin/.openclaw/workspace/atmApi/data/atmapi.db"
BACKUP_DIR="/home/admin/.openclaw/workspace/atmApi/backups"
DATE=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="$BACKUP_DIR/atmapi_backup_$DATE.db"

# 创建备份目录
mkdir -p "$BACKUP_DIR"

# 执行备份
if [ -f "$DB_PATH" ]; then
    cp "$DB_PATH" "$BACKUP_FILE"
    
    # 压缩备份
    gzip "$BACKUP_FILE"
    
    echo "✅ 数据库备份完成: ${BACKUP_FILE}.gz"
    
    # 删除 30 天前的旧备份
    find "$BACKUP_DIR" -name "atmapi_backup_*.db.gz" -mtime +30 -delete
    echo "🗑️ 已清理 30 天前的旧备份"
else
    echo "❌ 数据库文件不存在: $DB_PATH"
    exit 1
fi
