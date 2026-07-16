# atmApi 项目详情说明

**最后更新**：2026-07-16  
**版本**：v2.4  
**项目状态**：✅ 生产运行中

---

## 一、项目概述

### 1.1 项目定位

atmApi 是一个企业级 AI API 智能网关，提供两条核心产品线：
- **deepseek-a4**：智能路由旗舰产品，支持 DeepSeek 系列模型
- **GLM-5.2**：企业级深度推理产品，支持智谱 GLM-5.2 模型

**核心价值**：
- 🚀 智能路由：根据请求复杂度自动选择最优模型（flash/pro/多模态）
- 💰 成本优化：响应缓存、单例共享、熔断保护，降低 86%+ 成本
- 🛡️ 高可用：双节点热备、四路渠道冗余、自动故障切换
- 📊 透明计费：按量计费、缓存命中不扣费、成本可追溯

### 1.2 技术栈

- **后端**：Go 1.21 + Gin + GORM
- **数据库**：MySQL 8.0（生产）/ MariaDB 10.5（开发）
- **缓存**：内存缓存 + 响应缓存（5分钟 TTL）
- **部署**：Docker + Nginx 负载均衡 + systemd
- **监控**：ECharts 可视化看板 + 成本分析系统

### 1.3 代码规模

- **Go 文件**：87 个
- **代码行数**：18,865 行
- **数据库表**：13 张
- **API 接口**：21 个
- **套餐档位**：13 个

---

## 二、核心架构

### 2.1 部署架构

```
              Nginx (小龙女) least_conn
             /              \
   小龙女 :3300        逍遥子 :3300
          \              /
      MySQL 8.0 (Docker, 小龙女)
```

**节点分配**：

| 节点 | IP | 角色 | 数据库 | 端口 |
|------|-----|------|--------|------|
| 艾隆 | 8.220.139.36 | 开发测试 | MariaDB 本地 | 3300 |
| 小龙女 | 47.103.149.238 | 生产 + Nginx + MySQL | MySQL 8.0 Docker | 3300 |
| 逍遥子 | 39.106.204.127 | 生产 | MySQL 远程（小龙女） | 3300 |

**⚠️ 铁律**：
- 三节点代码必须一致，否则 Nginx 轮询会导致交替成功/失败
- 开发环境禁止连接生产数据库
- 部署生产必须建国明确指令

### 2.2 环境隔离

| 环境 | 服务器 | 数据库 | 用途 |
|------|--------|--------|------|
| **开发** | 艾隆 :3300 | MariaDB 10.5 (atmapi) | 新功能开发、测试、调试 |
| **生产** | 小龙女+逍遥子 :3300 | MySQL 8.0 (小龙女 Docker) | 真实用户、真实 Token、真实交易 |

**操作规范**：
1. 开发新功能 → 在艾隆本机测试，用本地 MariaDB
2. 部署生产 → 必须建国明确指令才能 SSH 小龙女/逍遥子
3. 测试 Token → 开发环境自己创建测试 Token，绝不能用生产环境的真实 Token
4. 数据查询 → 生产数据只能通过 MCP `query_usage` 查询，不能直接操作数据库

### 2.3 请求处理流程

```
用户请求
  ↓
JWT 认证 + 限流检查（5h/日/月/RPM）
  ↓
模型路由（deepseek-a4 智能路由）
  ├─ 简单文本 → deepseek-v4-flash（70%）
  ├─ 复杂推理 → deepseek-v4-pro（20%）
  └─ 含图片 → qwen3.7-plus（10%）
  ↓
GLM-5.2 专属优化（如果是 GLM-5.2 套餐）
  ├─ 请求规范化 + 会话跟踪
  ├─ 响应缓存检查（命中则直接返回）
  ├─ 四路渠道路由（OpenRouter → 硅基流动 → 词元 → deepwl）
  ├─ 熔断保护（5xx 错误率 > 50% 自动切换）
  └─ 成本追踪（upstream_reported > pricing_snapshot > fallback）
  ↓
上游 API 调用
  ↓
响应返回 + 用量记录 + 成本计算
```

---

## 三、产品线说明

### 3.1 deepseek-a4（次数制）

**定位**：智能路由旗舰产品，一模型通吃

**核心特性**：
- 三模型自动切换（flash/pro/多模态）
- 次数制计费（每次扣 1 次）
- 1,000,000 tokens 上下文（1M）
- 支持图片理解

**套餐档位**（6 档）：

| 套餐 | 月价 | 每 5 小时 | 月总量 | 适合人群 |
|------|------|----------|--------|----------|
| 基础版 | ¥29.9 | 100 次 | 8,000 次 | 个人尝鲜 |
| 专业版 | ¥69 | 300 次 | 24,000 次 | 日常开发 |
| 创业版 | ¥99 | 400 次 | 30,000 次 | 创业团队 |
| 旗舰版 | ¥129 | 600 次 | 50,000 次 | 重度用户 |
| 高级版 | ¥299 | 1,200 次 | 100,000 次 | 企业应用 |
| 企业版 | ¥599 | 3,000 次 | 300,000 次 | 大规模部署 |

**OpenClaw 专属套餐**（2 档）：

| 套餐 | 月价 | 每 5 小时 | 月总量 |
|------|------|----------|--------|
| OpenClaw 基础 | ¥99 | 500 次 | 30,000 次 |
| OpenClaw 进阶 | ¥299 | 2,000 次 | 100,000 次 |

**Hermes 专属套餐**（2 档）：

| 套餐 | 月价 | 每 5 小时 | 月总量 |
|------|------|----------|--------|
| Hermes 基础 | ¥149 | 800 次 | 50,000 次 |
| Hermes 进阶 | ¥399 | 3,000 次 | 150,000 次 |

### 3.2 GLM-5.2（点数制）

**定位**：企业级深度推理产品

**核心特性**：
- 点数制计费（按 token 用量扣点）
- 128,000 tokens 上下文（128K）
- 四渠道智能路由（OpenRouter → 硅基流动 → 词元 → deepwl）
- 熔断保护 + 响应缓存（命中率 86%+）
- 缓存命中不扣费

**套餐档位**（3 档）：

| 套餐 | 月价 | 每 5 小时 | 最大输入 | 最大输出 | 适合人群 |
|------|------|----------|----------|----------|----------|
| 体验版 | ¥49.9 | 500 次 | 32K | 8K | 个人开发者 |
| 标准版 | ¥128 | 1,500 次 | 64K | 16K | 小团队 |
| 专业版 | ¥298 | 3,000 次 | 128K | 32K | 企业级 |

**点数计算公式**：
```
每次扣点 = ⌈输入 tokens × 0.008 + 输出 tokens × 0.028⌉
```

**成本来源优先级**：
1. `upstream_reported`：OpenRouter 实报成本（最准确）
2. `pricing_snapshot`：价格快照估算（兜底）
3. `local_response_cache`：本地缓存命中（零成本）
4. `singleflight_shared`：单例共享请求（零成本）

---

## 四、技术实现

### 4.1 模块结构

```
internal/
├── api/                      # API 层（15 个文件）
│   ├── routes.go             # 路由注册 + 核心逻辑（2639 行）
│   ├── dashboard_v2.go       # 增强仪表盘（含 GLM-5.2 成本统计）
│   ├── cost_dashboard.go     # 成本看板（alerts/ranking/enhanced）
│   ├── mcp_handler.go        # MCP 工具（create_renewal/query_usage）
│   ├── payment_handler.go    # 支付宝支付
│   ├── plan_handler.go       # 套餐管理
│   ├── cache_handler.go      # 缓存分析
│   ├── prompt_handler.go     # Prompt 分析
│   ├── user.go               # 用户管理
│   └── alipay.go             # 支付宝 SDK
├── glmoptimizer/             # GLM-5.2 专属优化模块（23 个文件）
│   ├── router.go             # 智能路由
│   ├── breaker.go            # 熔断器
│   ├── cache.go              # 响应缓存
│   ├── context.go            # 上下文预算管理
│   ├── terminal.go           # 终态分类器
│   ├── canonical.go          # 请求规范化
│   ├── session.go            # 会话跟踪
│   ├── sse.go                # SSE 流式转发
│   ├── budget.go             # Token 预算控制
│   └── failure.go            # 失败分类器
├── model/                    # 数据模型（17 个文件）
│   ├── token.go              # Token 模型
│   ├── usage_log.go          # 用量日志（含 GLM-5.2 审计字段）
│   ├── provider_pricing.go   # Provider 价格快照
│   └── db.go                 # 数据库初始化
├── service/                  # 业务服务
│   ├── rate_limiter.go       # 限流（5h/日/月/RPM）
│   ├── cache.go              # 缓存服务
│   ├── smart_router.go       # 智能路由
│   └── cost_calculator.go    # 成本计算
├── config/                   # 配置加载
└── middleware/               # 中间件（JWT 认证 + 管理员）
```

### 4.2 数据库设计

**13 张核心表**：

| 表名 | 用途 | 关键字段 |
|------|------|----------|
| `tokens` | API Token 管理 | key, plan_name, rate_limit_group, plan_group |
| `channels` | 上游渠道配置 | name, base_url, api_key, priority, status |
| `plans` | 套餐定义 | name, display_name, price, hourly_5_max, monthly_max |
| `usage_logs` | 用量日志（核心） | token_id, plan_name, model, tokens, cost_amount, cost_source |
| `request_logs` | 请求日志 | token_id, model, duration, status_code |
| `orders` | 订单管理 | token_id, plan_name, amount, pay_url, status |
| `rate_limits` | 限流记录 | token_id, window_type, used_count |
| `glm_points_ledger` | GLM-5.2 点数账本 | token_id, period, total_points, used_points |
| `model_pricings` | 模型定价 | model, input_price, output_price |
| `prompt_segments` | Prompt 分段 | token_id, segment_hash, hit_count |
| `image_usages` | 图片用量 | token_id, image_count |
| `users` | 用户管理 | username, password_hash, role |
| `prompt_profiles` | Prompt 配置 | name, segments |

**关键字段说明**（usage_logs）：

```sql
-- GLM-5.2 审计字段（20+ 个）
cost_source          VARCHAR(50)   -- 成本来源（upstream_reported/pricing_snapshot/local_response_cache/singleflight_shared）
upstream_provider    VARCHAR(100)  -- 上游提供商（OpenRouter/硅基流动/词元/deepwl）
cost_amount          DECIMAL(10,6) -- 成本金额（CNY）
cost_currency        VARCHAR(20)   -- 成本币种（CNY/OPENROUTER_CREDITS）
reasoning_tokens     BIGINT        -- 推理 token 数
ttft_ms              BIGINT        -- 首 token 延迟（ms）
local_response_cache_hit  BOOLEAN  -- 本地缓存命中
session_id_hash_prefix   VARCHAR(20) -- 会话 ID 哈希前缀
terminal_state       VARCHAR(50)   -- 终态分类（success/error/timeout/breaker）
```

### 4.3 API 接口（21 个）

**核心接口**：
- `POST /v1/chat/completions` - 聊天完成（核心）
- `GET /api/v1/stats` - 系统统计
- `GET /api/v1/models` - 模型列表

**Token 管理**：
- `GET/POST/PUT/DELETE /api/v1/tokens` - Token CRUD
- `POST /api/v1/tokens/batch` - 批量创建

**套餐管理**：
- `GET/POST/PUT/DELETE /api/v1/plans` - 套餐 CRUD
- `GET /api/v1/pricing` - 定价查询

**渠道管理**：
- `GET/POST/PUT/DELETE /api/v1/channels` - 渠道 CRUD

**成本分析**：
- `GET /api/v1/cost-summary` - 成本汇总
- `GET /api/v1/cost-trend` - 成本趋势
- `GET /api/v1/cost-by-plan` - 按套餐统计
- `GET /api/v1/alerts` - 告警列表
- `GET /api/v1/dashboard/v2` - 增强仪表盘
- `GET /api/v1/token-ranking` - Token 排行
- `GET /api/v1/token/:id/cost` - 单 Token 成本

**MCP 工具**：
- `POST /api/v1/mcp/create_renewal` - 创建续费订单
- `POST /api/v1/mcp/query_usage` - 查询用量

**页面路由**：
- `/cost-dashboard` - 成本监控看板
- `/dashboard` - 管理后台
- `/monitor` - 监控中心
- `/token-info` - Token 查询页面

### 4.4 核心特性实现

#### 4.4.1 智能路由（deepseek-a4）

```go
// 根据请求复杂度自动选择模型
if hasImage(req) {
    model = "qwen3.7-plus"      // 10% 多模态
} else if isComplex(req) {
    model = "deepseek-v4-pro"   // 20% 深度推理
} else {
    model = "deepseek-v4-flash" // 70% 快速响应
}
```

#### 4.4.2 四路渠道路由（GLM-5.2）

```go
// 渠道优先级
channels := []Channel{
    {Name: "OpenRouter", Priority: 1},    // 最便宜 + 最稳定
    {Name: "硅基流动", Priority: 2},      // 稳定 + 合规
    {Name: "词元", Priority: 3},          // 原有渠道
    {Name: "deepwl", Priority: 4},        // 备用
}

// 熔断保护
if errorRate > 0.5 {  // 5xx 错误率 > 50%
    switchToNextChannel()
}
```

#### 4.4.3 响应缓存

```go
// 缓存策略
cacheKey := hash(request.Body)
if cached, ok := cache.Get(cacheKey); ok {
    // 缓存命中，直接返回，成本 = 0
    return cached.Response
}

// 缓存未命中，调用上游
response := callUpstream(request)
cache.Set(cacheKey, response, 5*time.Minute)
```

#### 4.4.4 成本追踪

```go
// 成本来源优先级
if upstreamReported != 0 {
    cost = upstreamReported  // OpenRouter 实报（最准确）
} else if pricingSnapshot != 0 {
    cost = pricingSnapshot   // 价格快照估算（兜底）
} else {
    cost = 0                 // 缓存命中 / 单例共享
}
```

---

## 五、成本监控系统

### 5.1 核心指标

- **总收入**：按调用次数比例分摊套餐收入
- **总成本**：上游 API 调用成本
- **总利润**：收入 - 成本
- **毛利率**：利润 / 收入 × 100%

### 5.2 成本分析维度

- **按 Token**：每个 Token 的收入、成本、利润
- **按套餐**：每个套餐的调用次数、成本、平均成本
- **按模型**：每个模型的调用次数、成本
- **按渠道**：每个上游渠道的成本分布
- **按时间**：每日成本趋势

### 5.3 告警机制

- **亏损告警**：Token 利润 < -¥10
- **高成本告警**：单次调用成本 > ¥1
- **异常告警**：错误率 > 10%

### 5.4 可视化看板

- **总览面板**：核心指标 + Token 排行 + 每日趋势
- **成本监控**：成本来源分布 + Token 明细 + 上游提供商
- **缓存分析**：缓存命中率 + 节省成本 + Prompt 结构分析

---

## 六、竞品对比

### 6.1 vs 套皮类产品

| 维度 | atmApi | 套皮类产品 |
|------|--------|-----------|
| 模型真实性 | ✅ 真 GLM-5.2 | ❌ 套皮其他模型 |
| 数据合规 | ✅ 数据不出境 | ❌ 数据出境 |
| 上下文长度 | ✅ 128K 完整 | ❓ 未知 |
| 价格 | ✅ ¥49.9 起 | ❌ ¥99/月 |

### 6.2 vs 阉割类产品

| 维度 | atmApi | 阉割类产品 |
|------|--------|-----------|
| 上下文长度 | ✅ 128K 完整 | ❌ 32K（1/4） |
| 多模态 | ✅ 支持 | ❌ 已关闭 |
| 并发能力 | ✅ 高并发 | ❌ 仅 3 路 |
| 隐藏注入 | ✅ 无 | ❌ 每次注入 640 tokens |
| 价格 | ✅ ¥49.9 起 | ❌ ¥99/月 |

**核心卖点**：别人阉割卖 ¥99，我们完整卖 ¥49.9

---

## 七、发展历程

### 2026-07-02：商业化落地
- deepseek-a4 套餐上线
- 次数制计费系统
- 三模型智能路由

### 2026-07-11：MySQL 迁移
- SQLite → MySQL 8.0
- 双节点共享数据库
- Nginx 负载均衡

### 2026-07-12：GLM-5.2 套餐启动
- 四渠道智能路由
- 熔断保护 + 响应缓存
- 点数制计费系统

### 2026-07-14：成本监控系统
- 成本追踪 + 审计字段
- 可视化看板
- 告警机制

### 2026-07-16：套餐线统一
- 套餐线统一为 3 种（deepseek-a4 / glm-5.2 / 未分类）
- GLM-5.2 专区图表优化
- 管理后台快捷链接

---

## 八、未来规划

### 8.1 短期（1-2 周）
- [ ] GLM-5.2 试运营样本收集
- [ ] 缓存优化策略（基于数据分析）
- [ ] 成本监控增强（按小时统计）

### 8.2 中期（1-2 月）
- [ ] 多模型支持（Claude、GPT-4）
- [ ] 企业级功能（团队管理、权限控制）
- [ ] API 市场（第三方模型接入）

### 8.3 长期（3-6 月）
- [ ] 自动化运维（自动扩缩容、故障自愈）
- [ ] AI 助手（智能客服、使用建议）
- [ ] 生态建设（开发者社区、插件市场）

---

## 九、相关文档

- **架构文档**：`ARCHITECTURE.md`
- **API 文档**：`/api-docs`（管理后台）
- **帮助文档**：`/help`（用户帮助）
- **飞书知识库**：[atmApi 项目文件夹](https://jcngahhy3yr0.feishu.cn/drive/folder/BqlffnjmYlbyIUddAHhcjnFun4f)

---

**文档维护**：艾隆（Elon）  
**联系方式**：建国（高建国）  
**版本历史**：GitHub `feat/glm-5.2-v1.1` 分支
