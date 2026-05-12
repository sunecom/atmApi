#!/bin/bash
# atmApi 一键部署脚本

echo "=== atmApi 一键部署 ==="

# 1. 安装 systemd 服务
echo "1. 配置 systemd 服务..."
sudo cp atmapi.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable atmapi
sudo systemctl start atmapi

# 2. 配置 Nginx（可选）
if [ "$1" = "--nginx" ]; then
    echo "2. 配置 Nginx 反向代理..."
    sudo cp nginx.conf /etc/nginx/sites-available/atmapi
    sudo ln -sf /etc/nginx/sites-available/atmapi /etc/nginx/sites-enabled/
    sudo nginx -t && sudo systemctl restart nginx
fi

# 3. 配置定时任务
echo "3. 配置定时任务..."
(crontab -l 2>/dev/null; echo "0 2 * * * $(pwd)/backup.sh") | crontab -
(crontab -l 2>/dev/null; echo "*/5 * * * * /usr/local/bin/port-check.sh") | crontab -
(crontab -l 2>/dev/null; echo "*/2 * * * * $(pwd)/monitor-alert.sh") | crontab -

# 4. 配置日志轮转
echo "4. 配置日志轮转..."
sudo cp atmapi.logrotate /etc/logrotate.d/atmapi

echo ""
echo "✅ 部署完成！"
echo "服务状态：sudo systemctl status atmapi"
echo "访问地址：http://localhost:3002/"
