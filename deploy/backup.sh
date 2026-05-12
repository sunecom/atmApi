#!/bin/bash
# atmApi 数据库备份脚本

BACKUP_DIR="/home/admin/.openclaw/workspace/atmApi/backups"
DB_FILE="/home/admin/.openclaw/workspace/atmApi/data/atmapi.db"
DATE=$(date +%Y%m%d_%H%M%S)

mkdir -p $BACKUP_DIR
cp $DB_FILE $BACKUP_DIR/atmapi_$DATE.db

# 删除 30 天前的备份
find $BACKUP_DIR -name "atmapi_*.db" -mtime +30 -delete

echo "[$(date)] 备份完成：atmapi_$DATE.db"
