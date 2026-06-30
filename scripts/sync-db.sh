#!/bin/bash
# =======================================================
# atmApi 数据库同步脚本
# =======================================================
# 从主节点（艾隆）同步 SQLite 数据库到各负载均衡节点
#
# 用法：
#   ./scripts/sync-db.sh              # 同步到所有节点
#   ./scripts/sync-db.sh xiaolongnv   # 仅同步到小龙女
#   ./scripts/sync-db.sh xiaoyaozi    # 仅同步到逍遥子
# =======================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
DB_FILE="$SCRIPT_DIR/data/atmapi.db"
BIN_FILE="$SCRIPT_DIR/atmapi"

# 节点清单
declare -A NODES
NODES["xiaolongnv"]="47.103.149.238|3300|Wybzdl886"
NODES["xiaoyaozi"]="39.106.204.127|3300|Wybzdl886"

# 当前时间
TIMESTAMP=$(date "+%Y%m%d_%H%M%S")

sync_one() {
    local name="$1"
    local info="${NODES[$name]}"
    local ip port pass
    ip=$(echo "$info" | cut -d'|' -f1)
    port=$(echo "$info" | cut -d'|' -f2)
    pass=$(echo "$info" | cut -d'|' -f3)

    echo "=== 同步到 $name ($ip:$port) ==="

    # 1. 先验证主节点数据库健康
    sqlite3 "$DB_FILE" "SELECT COUNT(*) FROM tokens WHERE status=1;" > /dev/null 2>&1 || {
        echo "❌ 主节点数据库文件损坏"
        return 1
    }

    # 2. 传输数据库文件
    echo "  📦 传输数据库..."
    rsync -avz --progress -e "sshpass -p '$pass' ssh -o StrictHostKeyChecking=no" \
        "$DB_FILE" "root@$ip:/tmp/atmapi.db.$TIMESTAMP" || {
        echo "  ⚠️ rsync 失败，尝试 scp..."
        scp -o StrictHostKeyChecking=no "$DB_FILE" "root@$ip:/tmp/atmapi.db.$TIMESTAMP" || {
            echo "  ⚠️ scp 也失败，分块传输..."
            split -b 512K "$DB_FILE" /tmp/atmapi-db-sync-
            for f in /tmp/atmapi-db-sync-*; do
                part=$(basename $f)
                cat $f | sshpass -p "$pass" ssh -o StrictHostKeyChecking=no "root@$ip" \
                    "su - admin -c 'cat >> /tmp/atmapi.db.$TIMESTAMP'" 2>/dev/null
                echo "    sent $part"
            done
            rm -f /tmp/atmapi-db-sync-*
        }
    }

    # 3. 部署
    sshpass -p "$pass" ssh -o StrictHostKeyChecking=no "root@$ip" \
        "su - admin -c '
            # 停服务
            sudo systemctl stop atmapi

            # 备份旧数据库
            cp ~/.openclaw/workspace/atmApi/data/atmapi.db ~/.openclaw/workspace/atmApi/data/atmapi.db.bak.\$(date +%Y%m%d_%H%M%S)

            # 部署新数据库
            cp /tmp/atmapi.db.$TIMESTAMP ~/.openclaw/workspace/atmApi/data/atmapi.db
            chmod 644 ~/.openclaw/workspace/atmApi/data/atmapi.db

            # 起服务
            sudo systemctl start atmapi
            echo \"✅ 部署完成\"
        '" 2>&1

    echo ""
}

# ===== 主逻辑 =====
echo "🔁 atmApi 数据库同步 - $TIMESTAMP"
echo "=========================="
echo ""

if [ $# -eq 0 ]; then
    # 同步到所有节点
    for name in "${!NODES[@]}"; do
        sync_one "$name"
    done
else
    # 同步到指定节点
    while [ $# -gt 0 ]; do
        sync_one "$1"
        shift
    done
fi

echo ""
echo "✅ 同步完成"
echo ""
echo "💡 下次同步: 在 WebUI 修改 Token/渠道后，运行此脚本"
