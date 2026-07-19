# 🏧 ATM API — AI API 智能网关

> **你的 AI 模型自动提款机**  
> AiToMoney 出品 🚀

[![Status](https://img.shields.io/badge/status-v2.1.0-success)](https://github.com/sunecom/atmApi)
[![License](https://img.shields.io/badge/license-MIT-green)](https://github.com/sunecom/atmApi)
[![Go](https://img.shields.io/badge/go-1.22-purple)](https://github.com/sunecom/atmApi)

---

## 📋 项目总览

atmApi 是 **AI API 智能网关**，核心能力是模型选择层——不是简单转发，而是根据场景智能路由到最优模型。

### 一句话定位

> 一个 API 入口（`deepseek-a4`），自动选最优模型 + 最低成本。

### 核心能力

| 能力 | 说明 | 效果 |
|------|------|------|
| 🧠 **智能路由** | 按图片/复杂度/上下文自动选模型 | 70% flash + 20% pro + 10% 视觉 |
| 💰 **成本优化** | 多渠道 failover + 缓存 + 压缩 | 节省 60%+ |
| 🔒 **会话偏好** | session 级模型锁定，不跨会话串扰 | 体验流畅 |
| 🖼️ **图片路由** | 检测到图片自动切 qwen3-vl-plus | 专业视觉识别 |
| 🚦 **限流熔断** | 滑动窗口 + 渠道熔断 | 保护上游 |
| 📊 **成本监控** | 实时仪表盘 + 三级优先级追踪 | 成本透明 |

---

## 🏗️ 架构简图

```
用户 → deepseek-a4
    │
    ▼ SmartRoute
    ├─ 有图片？ → qwen3-vl-plus（百炼视觉模型）
    ├─ 有偏好？ → 复用偏好模型
    ├─ 工具事务？ → pro（深度推理）
    └─ 简单对话？ → flash（快速便宜）
```

### 部署架构

```
Nginx (小龙女) least_conn
    ↙          ↘
小龙女:3300   逍遥子:3300
    ↖          ↗
  MySQL 8.0 (Docker, 小龙女)
```

---

## 🚀 快速开始

### 开发环境

```bash
# 克隆
git clone https://github.com/sunecom/atmApi.git
cd atmApi

# 构建
go build -o atmapi .

# 运行（需要数据库和 channel 配置）
./atmapi
```

### 测试

```bash
# 对话测试
curl -X POST http://localhost:3300/v1/chat/completions \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-a4","messages":[{"role":"user","content":"你好"}]}'

# 图片测试
curl -X POST http://localhost:3300/v1/chat/completions \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-a4","messages":[{"role":"user","content":[{"type":"text","text":"描述图片"},{"type":"image_url","image_url":{"url":"https://example.com/photo.jpg"}}]}]}'

# 健康检查
curl localhost:3300/health
```

---

## 📦 产品套餐

| 套餐 | 价格 | 限额 | 适用场景 |
|------|------|------|---------|
| 🥉 基础版 | ¥29.9/月 | 500次/5h, 6000次/月 | 轻度用户 |
| 🥈 专业版 | ¥49.9/月 | 2000次/5h, 25000次/月 | 个人开发者 |
| 🥇 旗舰版 | ¥89/月 | 5000次/5h, 60000次/月 | 重度用户 |

**路由成本**：flash 约 ¥0.55/M → pro 约 ¥3.13/M → 视觉约 ¥1/M

---

## 📊 项目里程碑

| 时间 | 里程碑 | 说明 |
|------|--------|------|
| 2026-06 初 | v1.0 MVP | 基础路由 + 渠道管理 |
| 2026-07-07 | deepseek-a4 路由 | 智能路由决策引擎 |
| 2026-07-11 | MySQL 迁移 | SQLite → MySQL 双节点共享 |
| 2026-07-12 | OpenRouter 引入 | 三条 OpenRouter 线路 + 2000 并发测试 |
| 2026-07-14 | GLM-5.2 套餐 | 四渠道路由 + 成本追踪 |
| 2026-07-18 | **V1.7 关单** ✅ | 7 轮柯大侠复核，38 测试全绿 |
| 2026-07-19 | qwen3-vl-plus 视觉路由 | 百炼专业视觉模型接入 |

### V1.7 核心改进（上下文治理）

| 改进前 | 改进后 |
|--------|--------|
| "继续"→模型跳回 Flash 变笨 | 同一会话保持同一模型 |
| 50% 就删历史→失忆 | shadow 模式不裁剪 |
| 多群聊互相串线 | 会话级隔离 |
| 工具调用中途换模型→格式乱 | 工具链全程不切换 |
| 发图后卡在 Qwen | 图片只是临时路由 |

---

## 🧪 测试

38 个测试全绿（含 race 检测）：

```bash
go test ./... -count=1 -timeout 120s
```

关键测试覆盖：
- SmartRoute 决策（7 个用例）
- SSE 终态分类（10+ 用例）
- 会话偏好集成（偏好写入/复用/断流不写）
- 图片路由（不写偏好）

---

## 🛠️ 技术栈

| 层 | 技术 |
|------|------|
| 语言 | Go 1.22 |
| Web 框架 | Gin |
| 数据库 | MySQL 8.0 (Docker) / GORM |
| 前端 | 原生 HTML + JS |
| 部署 | systemd + Nginx |
| CI | GitHub Actions（计划中） |

---

## 📁 相关文档

| 文档 | 位置 |
|------|------|
| 完整架构文档 | `skills/atmapi/ARCHITECTURE.md` |
| 部署 SOP | `skills/atmapi/DEPLOY-SOP.md` |
| GLM-5.2 实现总结 | `skills/atmapi/GLM52-COMPLETION.md` |
| 上下文治理 V1.7 | `skills/atmapi/GLM52-CONTEXT-V1.1.md` |
| DeepSeek 并发测试 | `skills/atmapi/DEEPSEEK-TEST-REPORT.md` |

---

## 👥 团队

- **建国** — 项目指导，决策支持
- **柯大侠** — 核心代码开发（V1.7 + GLM-5.2）
- **艾隆** — 独立复核，测试验证，部署维护

---

## 📄 License

[MIT](https://github.com/sunecom/atmApi/blob/main/LICENSE)