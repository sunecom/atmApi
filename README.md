<p align="center">
  <img src="https://img.shields.io/badge/status-v0.1.0-success" alt="Status">
  <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  <img src="https://img.shields.io/badge/go-1.22-purple" alt="Go">
  <img src="https://img.shields.io/badge/AiToMoney-%F0%9F%A6%90-orange" alt="AiToMoney">
</p>

<h1 align="center">🏧 ATM API</h1>
<p align="center"><b>你的 AI 模型自动提款机</b></p>

<p align="center">
  🇨🇳 全中文后台 · 🔄 多渠道自动 Fallback · 🐳 一键部署<br/>
  <i>One API 的国产替代 Plus — 更贴近中国开发者的使用习惯</i>
</p>

<p align="center">
  <a href="#-快速开始">快速开始</a> ·
  <a href="#-核心功能">核心功能</a> ·
  <a href="#-技术架构">技术架构</a> ·
  <a href="#-配置指南">配置指南</a>
</p>

---

## 📸 截图

> 管理后台预览（等待补充截图）：
> ![后台首页](https://via.placeholder.com/800x500/1e1e2e/4f46e5?text=atmApi+Admin+Dashboard)

---

## 🚀 快速开始

### 方式一：直接运行（推荐）

```bash
# 1. 下载二进制
wget https://github.com/sunecom/atmApi/releases/latest/download/atmapi
chmod +x atmapi

# 2. 启动（默认端口 3000）
PORT=3002 ./atmapi

# 3. 访问
open http://localhost:3002
```

### 方式二：Docker 部署

```bash
docker run -d --name atmapi -p 3002:3002 \
  -v ./data:/app/data \
  sunecom/atmapi:latest
```

### 方式三：从源码编译

```bash
git clone https://github.com/sunecom/atmApi.git
cd atmApi
go build -o atmapi .
./atmapi
```

### 默认账号

```
用户名：admin
密码：admin123
```

---

## 💡 核心功能

### 1. 多渠道统一接入

将通义千问、DeepSeek、GoToken 等多个上游模型服务统一纳入同一个系统管理。

```
请求 → ATM API → [通义千问] ← 主渠道
               → [DeepSeek]  ← 自动 Fallback
```

**好处**：一个入口管理所有模型，上游切换零成本。

### 2. 自动 Fallback

当主渠道不可用时（4xx/5xx/超时），自动切换到备用渠道。

```
主渠道通义千问挂了 → 自动切 DeepSeek → 还不行切 GoToken
```

**效果**：模型服务不稳定时，业务不停。

### 3. 全中文管理后台

- 渠道管理（增删改查 + 编辑 + 在线测试）
- Token 管理（创建 + 配额控制 + 搜索）
- 用户管理（角色权限）
- 请求日志（查看 + 导出 CSV + 时间筛选）
- 用量统计（含图表）
- 模型测试（在线调试）
- 一键启动，开箱即用

### 4. Token 配额管理

- 为每个调用方生成独立的 API Token
- 支持无限配额 / 有限配额
- 支持过期时间设置
- 方便做用量统计和权限控制

---

## 🏗 技术架构

```
┌──────────────────────┐
│     HTTP 请求入口     │
│  localhost:3002       │
└──────────┬───────────┘
           ▼
┌──────────────────────┐
│    Gin 路由层         │
│  Auth / Middleware    │
└──────────┬───────────┘
           ▼
┌──────────────────────┐
│    业务逻辑层          │
│  渠道管理 / Token管理  │
│  模型路由 / Fallback   │
└──────────┬───────────┘
           ▼
┌──────────────────────┐
│    GORM + SQLite     │
│    (可切换 MySQL)     │
└──────────┬───────────┘
           ▼
┌──────────────────────┐
│   上游模型服务          │
│ 通义千问/DeepSeek/...  │
└──────────────────────┘
```

### 技术栈

| 层级 | 技术 |
|------|------|
| 后端框架 | Go + Gin |
| 数据库 | GORM + SQLite（支持 MySQL） |
| 认证 | JWT Token |
| 前端 | 原生 HTML + JS |
| 部署 | 二进制 / Docker |

---

## ⚙️ 配置指南

### 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | `3000` | 服务端口 |
| `DB_TYPE` | `sqlite` | 数据库类型（sqlite / mysql） |
| `DB_PATH` | `./data/atmapi.db` | 数据库路径 |
| `JWT_SECRET` | `atmapi-jwt-secret-2026` | JWT 签名密钥 |
| `LOG_LEVEL` | `info` | 日志级别 |

### 添加上游渠道

1. 登录后台（admin/admin123）
2. 进入「渠道管理」
3. 填写：
   - 渠道名称
   - API Key
   - Base URL
   - 支持的模型列表
   - 优先级和权重

### 模型映射

支持将请求模型名映射到实际渠道模型：

```json
{
  "qwen3.5-plus": "deepseek-v4-flash",
  "gpt-4": "qwen-turbo"
}
```

映射在「新增渠道」的「模型映射 JSON」字段中设置。

---

## 🔄 与 One API 对比

| 特性 | One API | ATM API |
|------|---------|---------|
| 界面语言 | 英文为主 | 全中文 |
| Fallback | 需手动配置 | 默认启用 |
| 部署复杂度 | 中等 | 极简 |
| 前端框架 | React | 原生 JS（轻量） |
| 默认数据库 | MySQL | SQLite |
| 团队品牌 | 无 | AiToMoney 出品 |
| 渠道在线测试 | 无 | ✅ |
| 模型映射可视化 | 无 | ✅ |

---

## 📝 功能清单

### ✅ 已实现
- [x] 多渠道管理（CRUD + 测试 + 模型映射可视化）
- [x] Token 管理（配额 + 过期时间 + 搜索）
- [x] 用户管理（角色权限）
- [x] 模型路由 + 自动 Fallback
- [x] 请求日志（查看 + 导出 CSV + 时间筛选）
- [x] 用量统计（含图表）
- [x] 系统设置
- [x] API 文档页
- [x] 批量操作
- [x] 快捷键支持（Ctrl+1-9）
- [x] 页面加载动画

### 🚧 计划中
- [ ] 更多图表类型
- [ ] 性能优化
- [ ] 单元测试
- [ ] 国际化支持

---

## 🦐 关于 AiToMoney

<p align="center">
  <b>一个人可以走得很快，一群虾可以折腾得更远</b>
</p>

**AiToMoney 虾主联盟** — 由一群不满足于打工、用 AI 技术创造真实价值的实践者组成。

| 平台 | 信息 |
|------|------|
| **QQ 群** | 242249487 |
| **入群暗号** | "我是一只虾，正在水里瞎折腾" |

---

## 📄 许可证

[MIT License](LICENSE)

<p align="center">
  Made with 🦐 by <a href="https://www.aitomoney.online">AiToMoney 虾主联盟</a>
</p>

---

## 🚀 生产环境部署

### 快速部署（推荐）

```bash
# 1. 克隆仓库
git clone https://github.com/sunecom/atmApi.git
cd atmApi

# 2. 配置 systemd 服务
sudo cp deploy/atmapi.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable atmapi
sudo systemctl start atmapi

# 3. 配置 Nginx 反向代理（HTTPS）
sudo cp deploy/nginx.conf /etc/nginx/sites-available/atmapi
sudo ln -s /etc/nginx/sites-available/atmapi /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl restart nginx

# 4. 配置定时备份
(crontab -l 2>/dev/null; echo "0 2 * * * /path/to/atmApi/deploy/backup.sh") | crontab -
```

### 性能指标

| 接口 | 100 次请求耗时 | 平均响应时间 |
|------|--------------|-------------|
| /health | ~0.7s | ~7ms |
| /api/v1/login | ~0.4s (50 次) | ~8ms |
| /api/v1/channels | ~0.7s | ~7ms |

### 监控与维护

```bash
# 查看服务状态
sudo systemctl status atmapi

# 查看日志
tail -f data/atmapi.log

# 手动备份
./deploy/backup.sh

# 性能压测
./deploy/benchmark.sh
```

详细文档见 [deploy/README.md](deploy/README.md)

---

## 🔒 高可用保障策略

### 三级保障体系

| 级别 | 策略 | 效果 |
|------|------|------|
| **L1 进程级** | systemd + 守护脚本 | 崩溃 30 秒内自动恢复 |
| **L2 监控级** | 外部监控 + 告警 | 异常时第一时间通知 |
| **L3 数据级** | 定时备份 + 日志轮转 | 数据不丢失，磁盘不爆 |

### 已配置项

- ✅ systemd 服务（开机自启 + 崩溃重启）
- ✅ 端口监控（每 5 分钟检查）
- ✅ 外部监控告警（每 2 分钟检查，连续失败 3 次告警）
- ✅ 数据库备份（每天凌晨 2 点）
- ✅ 日志轮转（每天轮转，保留 30 天）
- ✅ 一键部署脚本

### 可选配置

- HAProxy 负载均衡（多实例部署）
- Nginx 反向代理（HTTPS）
- 告警通知（邮件/企微/飞书/短信）

详细文档见 [deploy/README.md](deploy/README.md)
