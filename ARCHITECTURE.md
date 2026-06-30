# atmApi 架构文档 v3.0 (双保险版)

> 更新时间：2026-06-14
> AiToMoney 生态的模型路由中台

---

## ⚠️ 项目生命线

```bash
# 0. 更新阿里百炼 API Key（手动操作）
# 将新 Key 写入 ~/.openclaw/workspace/atmApi/.env
# 然后执行：sqlite3 data/atmapi.db "UPDATE channels SET key = '$(grep DASHSCOPE_API_KEY .env | cut -d= -f2)' WHERE id = 1;"

# 1. 编译（改代码后必做）
cd /home/admin/.openclaw/workspace/atmApi && go build -o atmapi .

# 2. 重启服务（编译后必做）
pkill -9 -f "atmApi/atmapi" || true && sleep 2 && PORT=3002 nohup ./atmapi > /tmp/atmapi.log 2>&1 &

# 3. 查看日志/排错
tail -f /tmp/atmapi.log

# 4. 操作数据库（手动修改渠道配置）
sqlite3 /home/admin/.openclaw/workspace/atmApi/data/atmapi.db
```

---

## 🏗️ 架构图 (双保险模式)

```plaintext
                    OpenClaw Gateway (:18489)
                            │
          ┌─────────────────┼──────────────────┐
          │                 │                  │
     艾隆服务器         小龙服务器           盖茨服务器...
          │                 │                  │
          └─────────────────┼──────────────────┘
                            │
                    ┌───────┴───────┐
                    │  atmApi :3002  │  ← 部署在艾隆服务器 (8.220.139.36)
                    └───────┬───────┘
                            │
          ┌─────────────────┼──────────────────┐
          │                 │                  │
   阿里百炼(Pro)        智谱 GLM           DeepSeek
    :443                   :443               :443
  P10 / 200元/月       P15 / 免费         P5 / 5元/天
   qwen3.7/3.6         glm-4.7-flash      deepseek-v4-flash
          │                 │                  │
          └─────────────────┼──────────────────┘
                            │
                     SQLite :data/atmapi.db
                    (日志/用量/成本统计)

🆘 终极灾备通道 (直连官方):
Gateway -> https://open.bigmodel.cn/api/paas/v4 (zhipu/glm-4.7-flash)
```

**核心原则**：
1.  **统一入口**：所有智能体请求均指向艾隆服务器上的 atmApi。
2.  **双保险**：如果 atmApi 宕机，Gateway 自动切换至直连智谱官方 API。

---

## 🔄 三层路由策略

```
请求任意模型
    ↓
P15 → 智谱 GLM-4.7-Flash (免费/200K上下文) ← **首选：无限流量强力兜底**
    ↓ 失败/限流
P10 → 阿里百炼 (200元/月 Pro 套餐)   ← 主力：性能最强
    ↓ 再失败
P5  → DeepSeek v4-flash (5元/天硬限额) ← 最终防线（限额告警 + 一键熔断）
    ↓ atmApi 彻底失联
🆘  → zhipu/glm-4.7-flash (直连官方) ← **终极灾备：业务永不掉线**
```

---

## 📊 关键数据结构 (`channels`)

| 字段 | 说明 |
|------|------|
| `name` | 渠道名（阿里/智谱/DeepSeek） |
| `key` | 上游 API Key |
| `base_url` | 上游端点 |
| `models` | 支持的模型列表（逗号分隔） |
| `status` | 1=启用, 2=禁用 |
| `priority` | 路由优先级（越大越优先） |
| `daily_limit` | 每日消费限额（元），0=不限制 |
| `daily_cost` | 今日累计消费（元） |

---

## 🛠️ 指令清单

| 指令 | 底层实现 | 踩坑记录 |
|------|---------|---------|
| 暂停 DeepSeek | `sqlite3 channels SET status=2 WHERE name LIKE '%DeepSeek%'` | ❌ 曾尝试通过 atmApi 直接发 QQ 消息，改为写文件 `/tmp/atmapi-deepseek-alert` 由 Gateway Cron 轮询 |
| 重置今日用量 | `sqlite3 channels SET daily_cost=0 WHERE id=N` | 无 |
| 检查 DeepSeek 告警 | 读 `/tmp/atmapi-deepseek-alert` 文件 | ✅ 改用 Gateway Cron (1分钟间隔) 读文件 + 回复当前 QQ 窗口 |
| 测试渠道连通性 | `curl 阿里/智谱/DeepSeek` | ❌ atmApi Web 页面的 `/v1/channels/:id/test` 需要认证 Token，监控页面 `/monitor` 使用免认证的 `/api/stats` 接口 |

---

## 💻 硬件/环境约束

| 参数 | 值 | 对项目的影响 |
|------|----|-------------|
| 服务器 | 8.220.139.36 (艾隆本机) | atmApi 部署在此 |
| 端口 | **3002** | Gateway 通过 localhost:3002 调用 atmApi |
| 数据库 | SQLite (`data/atmapi.db`) | 单机部署，不支持多节点 |
| Go 版本 | 项目要求 1.20+ | `go build` 即可编译 |

---

## 📜 踩坑简史

| # | 问题 | 解决 |
|---|------|------|
| 1 | DeepSeek 死循环烧钱，日消费 63 元 | 在 `channels` 表加 `daily_limit` 字段，路由层做限额拦截 |
| 2 | atmApi 无法直接发 QQ 告警 | 改为写文件 `/tmp/atmapi-deepseek-alert`，由 Gateway Cron 轮询 |
| 3 | 阿里渠道报 "HTTP 404" | ❌ `base_url`