# atmApi

**ATM API — 你的 AI 模型自动提款机**

atmApi 是 AiToMoney 团队开源的 AI 模型 API 管理平台，基于 One API 设计理念重构，专为国内开发者打造。

## ✨ 核心特性

- 🇨🇳 **全中文界面** - 国人习惯，开箱即用
- 🔄 **多渠道自动 Fallback** - 通义/DeepSeek/GLM 等国产模型自动切换
- 📊 **实时用量监控** - 图表 + 告警，用量一目了然
- 💳 **内置充值/账单** - 微信/支付宝扫码自动充值
- 🐳 **Docker 一键部署** - 一条命令搞定

## 🚀 快速开始

### Docker 部署（推荐）

```bash
# 一键启动
docker run -d --name atmapi -p 3000:3000 -v /data/atmapi:/data ghcr.io/sunecom/atmapi:latest

# 访问管理后台
# http://localhost:3000
# 默认账号：admin / admin123
```

### 本地开发

```bash
# 克隆项目
git clone https://github.com/sunecom/atmApi.git
cd atmApi

# 安装依赖
go mod download

# 启动服务
go run main.go

# 访问 http://localhost:3000
```

## 📚 技术栈

| 层级 | 技术 |
|------|------|
| 后端 | Go + Gin + GORM |
| 前端 | Vue3 + Element Plus（开发中） |
| 数据库 | SQLite（默认）/ MySQL |
| 部署 | Docker |

## 📁 项目结构

```
atmApi/
├── cmd/              # 入口文件
├── internal/         # 内部代码
│   ├── api/          # API 路由
│   ├── config/       # 配置管理
│   ├── model/        # 数据模型
│   ├── service/      # 业务逻辑
│   └── middleware/   # 中间件
├── web/              # 前端代码（开发中）
├── config/           # 配置文件
├── scripts/          # 脚本
└── docs/             # 文档
```

## 🔌 API 文档

### 基础端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 健康检查 |
| POST | `/api/v1/login` | 用户登录 |
| POST | `/api/v1/register` | 用户注册 |
| GET | `/api/v1/tokens` | 获取 Token 列表 |
| POST | `/api/v1/tokens` | 创建 Token |
| PUT | `/api/v1/tokens/:id` | 更新 Token |
| DELETE | `/api/v1/tokens/:id` | 删除 Token |
| GET | `/api/v1/channels` | 获取渠道列表 |
| POST | `/api/v1/channels` | 创建渠道 |
| PUT | `/api/v1/channels/:id` | 更新渠道 |
| DELETE | `/api/v1/channels/:id` | 删除渠道 |
| POST | `/api/v1/chat/completions` | 模型路由（核心功能） |
| GET | `/api/v1/models` | 列出可用模型 |

### 多渠道路由示例

```bash
# 请求会自动路由到可用渠道
curl -X POST http://localhost:3000/api/v1/chat/completions \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen3.5-plus",
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

## 📝 开发计划

| 阶段 | 时间 | 里程碑 |
|------|------|--------|
| MVP | 2026-05-18 | ✅ 基础 CRUD + 多渠道路由 |
| Beta | 2026-05-25 | 监控告警 + Docker 部署 |
| v1.0 | 2026-06-01 | 内置充值 + 账单管理 |

## 🤝 贡献

欢迎提交 Issue 和 PR！

## 📄 开源协议

MIT License

---

**AiToMoney 虾主联盟** 🦐
> 一个人可以走得很快，一群虾可以折腾得更远

QQ 群：242249487
