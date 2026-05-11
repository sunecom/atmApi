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

```bash
# Docker 一键启动
docker run -d --name atmapi -p 3000:3000 -v /data/atmapi:/data ghcr.io/sunecom/atmapi:latest

# 访问管理后台
# http://localhost:3000
# 默认账号：admin / admin123
```

## 📚 技术栈

| 层级 | 技术 |
|------|------|
| 后端 | Go + Gin + GORM |
| 前端 | Vue3 + Element Plus |
| 数据库 | SQLite（默认）/ MySQL |
| 部署 | Docker |

## 📁 项目结构

```
atmApi/
├── cmd/           # 入口文件
├── internal/      # 内部代码
│   ├── api/       # API 路由
│   ├── model/     # 数据模型
│   ├── service/   # 业务逻辑
│   └── middleware/ # 中间件
├── web/           # 前端代码
├── config/        # 配置文件
├── scripts/       # 脚本
└── docs/          # 文档
```

## 📝 开发计划

| 阶段 | 时间 | 里程碑 |
|------|------|--------|
| MVP | 2026-05-18 | 基础 CRUD + 多渠道路由 |
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
