# atmApi 生产环境部署指南

## 快速部署

### 1. 安装 Nginx（HTTPS 反向代理）

```bash
sudo apt install nginx
sudo cp deploy/nginx.conf /etc/nginx/sites-available/atmapi
sudo ln -s /etc/nginx/sites-available/atmapi /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl restart nginx
```

### 2. 配置 SSL 证书

```bash
# 使用 Let's Encrypt 免费证书
sudo apt install certbot python3-certbot-nginx
sudo certbot --nginx -d your-domain.com
```

### 3. 配置 systemd 服务

```bash
sudo cp deploy/atmapi.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable atmapi
sudo systemctl start atmapi
```

### 4. 配置定时备份

```bash
# 添加到 crontab（每天凌晨 2 点备份）
crontab -e
# 添加：0 2 * * * /home/admin/.openclaw/workspace/atmApi/deploy/backup.sh
```

### 5. 配置日志轮转

```bash
sudo cp deploy/atmapi.logrotate /etc/logrotate.d/atmapi
```

## 监控

### 健康检查

```bash
curl http://localhost:3002/health
```

### 性能压测

```bash
./deploy/benchmark.sh
```

### 查看日志

```bash
tail -f data/atmapi.log
```

## 备份恢复

### 手动备份

```bash
./deploy/backup.sh
```

### 恢复数据库

```bash
cp backups/atmapi_YYYYMMDD_HHMMSS.db data/atmapi.db
```

## 常见问题

### 服务无法启动

```bash
sudo systemctl status atmapi
sudo journalctl -u atmapi -n 50
```

### Nginx 报错

```bash
sudo nginx -t
sudo systemctl status nginx
```
