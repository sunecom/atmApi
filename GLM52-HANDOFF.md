# GLM-5.2 模块开发交接文档

**准备人**: 艾隆  
**接收人**: 柯大侠  
**日期**: 2026-07-13  
**版本**: v1.0

---

## 一、源码信息

### 1.1 Git 仓库
```
仓库地址: https://github.com/sunecom/atmApi
分支: main
最新 commit: 9776250 feat: 未完成的缓存分析/Prompt/路由改动同步
工作区状态: clean (无未提交更改)
```

### 1.2 同步方式
```bash
# 柯大侠本地已有 atmApi-source，执行：
cd atmApi-source
git fetch origin
git checkout main
git pull origin main
# 确认 commit: 9776250
```

---

## 二、开发环境

### 2.1 环境要求
| 项目 | 版本 | 备注 |
|------|------|------|
| Go | 1.22.5 | 必须一致，避免兼容性问题 |
| 数据库（开发） | MariaDB 10.5.29 | 本地开发用 |
| 数据库（生产） | MySQL 8.0.46 | Docker 部署在小龙女 |
| 端口 | 3300 | atmApi 默认端口 |
| 操作系统 | Linux (x86_64) | 开发/生产均为 Linux |

### 2.2 启动命令
```bash
cd atmApi
# 配置 .env（见第三节）
go run main.go
# 或编译后运行
go build -o atmapi main.go
./atmapi
```

---

## 三、数据库配置

### 3.1 .env 配置（开发环境）
```bash
# 数据库类型
DB_TYPE=mysql

# 开发环境连接（本地 MariaDB）
DB_PATH=atmapi:atmapi2026@tcp(127.0.0.1:3306)/atmapi?charset=utf8mb4&parseTime=True&loc=Local

# 其他配置
GIN_MODE=debug
JWT_SECRET=your-test-jwt-secret
ADMIN_KEY=your-test-admin-key
```

### 3.2 数据库初始化

#### 方案 A：使用 GORM 自动迁移（推荐）
```bash
# 1. 创建空数据库
mysql -u root -p -e "CREATE DATABASE atmapi_glm_test CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
mysql -u root -p -e "CREATE USER 'atmapi_test'@'localhost' IDENTIFIED BY 'atmapi2026';"
mysql -u root -p -e "GRANT ALL PRIVILEGES ON atmapi_glm_test.* TO 'atmapi_test'@'localhost';"
mysql -u root -p -e "FLUSH PRIVILEGES;"

# 2. 修改 .env
DB_PATH=atmapi_test:atmapi2026@tcp(127.0.0.1:3306)/atmapi_glm_test?charset=utf8mb4&parseTime=True&loc=Local

# 3. 启动应用（自动建表）
go run main.go
```

#### 方案 B：导入纯结构（不含数据）
```bash
# 导入 schema（已提供）
mysql -u atmapi_test -patmapi2026 atmapi_glm_test < /path/to/atmapi-schema.sql
```

**schema 文件位置**: `/tmp/atmapi-schema.sql`（282 行，10 张表结构）

---

## 四、GLM-5.2 现有配置

### 4.1 模型定价（已存在）
```sql
INSERT INTO model_pricings (model_name, input_price, output_price, provider) 
VALUES ('glm-5.2', 0.008, 0.028, 'zhipu');
```

**说明**:
- 输入价格: ¥0.008 / 千 tokens
- 输出价格: ¥0.028 / 千 tokens
- 供应商: 智谱官方

### 4.2 现有 GLM 渠道（8 个）

| ID | 名称 | 模型 | Base URL | 优先级 | 用途 |
|----|------|------|----------|--------|------|
| 5 | 智谱 GLM (兜底) | glm-5 | open.bigmodel.cn | 100 | 兜底 |
| 6 | 智谱 GLM-4.7 (免费) | glm-4.7 | open.bigmodel.cn | 90 | 免费测试 |
| 7 | 智谱 GLM-5.2 (cmkey.cn) | glm-5.2 | cmkey.cn | 80 | 第三方 |
| 9 | 智谱 Coding (官方团队版) | glm-5.2 | open.bigmodel.cn | 85 | 官方 |
| 12 | 词元 glm-5.2 | glm-5.2 | api.tokenriver.cn | 70 | 第三方 |
| 13 | deepwl glm-5.2 | glm-5.2 | zx1.deepwl.net | 60 | 第三方 |
| 18 | 硅基流动 GLM-5.2 | zai-org/GLM-5.2 | api.siliconflow.cn | 75 | 第三方 |
| 19 | OpenRouter GLM-5.2 | z-ai/glm-5.2 | openrouter.ai | 95 | **推荐主渠道** |

**渠道优先级说明**:
- 数字越大优先级越高
- OpenRouter (ID 19) 优先级 95，是最优选渠道
- 智谱官方 (ID 9) 优先级 85，作为备选

### 4.3 OpenRouter 渠道详情（核心）

**渠道 ID**: 19  
**模型映射**: `z-ai/glm-5.2`  
**Base URL**: `https://openrouter.ai/api/v1/chat/completions`  
**API Key**: 见 `/tmp/openrouter-key`（单独提供，不入库）

**定价（促销期 70% off）**:
- 输入: $0.42 / M tokens ≈ ¥3.02 / M tokens
- 输出: $1.32 / M tokens ≈ ¥9.50 / M tokens

**⚠️ 促销价风险**:
- 70% off 不可持续
- 恢复原价后比智谱官方还贵
- 成本基准应按硅基流动价格（¥6/¥24）计算

**并发能力（已验证）**:
- 2000 并发 / 4000 请求 / 100% 成功率
- max_active_inflight = 2000（真实在途）
- 排队等待 0.000s，无客户端瓶颈

---

## 五、GLM-5.2 套餐设计（待实现）

### 5.1 套餐定位
**目标**: 与 deepseek-a4 并列的独立套餐线

**现有套餐**:
- deepseek-a4 系列: basic / pro / flagship / starter / advanced / enterprise
- openclaw 系列: openclaw-basic / openclaw-pro
- hermes 系列: hermes-basic / hermes-pro

**GLM-5.2 套餐（待创建）**:
```
glm52-basic      ¥29.9/月
glm52-pro        ¥69/月
glm52-flagship   ¥129/月
```

### 5.2 套餐控制逻辑（待实现）

**Pro 调用控制**（参考 deepseek-a4）:
| 套餐 | Pro 调用比例上限 | 说明 |
|------|----------------|------|
| glm52-basic | 0% | 纯 Flash |
| glm52-pro | ≤10% | 大部分 Flash |
| glm52-flagship | ≤25% | 允许更多 Pro |

**超限处理**: 静默降级到 Flash，用户无感知

**实现位置**: `internal/service/router.go` 的 `selectModel()` 函数

### 5.3 路由逻辑（待实现）

**当前 deepseek-a4 路由**:
```go
// internal/service/router.go
func (r *SmartRouter) selectModel(token *model.Token, req *ChatRequest) string {
    // 1. 检查套餐
    plan := getPlanByToken(token.Name)
    
    // 2. 检查消息复杂度
    complexity := analyzeComplexity(req.Messages)
    
    // 3. 根据套餐限制选择模型
    if complexity == "complex" && plan.AllowPro() {
        return "deepseek-v4-pro"
    }
    return "deepseek-v4-flash"
}
```

**GLM-5.2 需要**:
- 新增 `glm52Router` 或在现有 router 中增加 GLM-5.2 分支
- 根据套餐名 `glm52-*` 判断是否走 GLM-5.2 渠道
- 实现 Pro 比例控制（类似 deepseek-a4）

---

## 六、测试渠道 Key（单独提供）

**⚠️ 安全原则**: 测试 Key 不写入 Git，单独传递

### 6.1 OpenRouter Key（已配置）
**文件位置**: `/tmp/openrouter-key`  
**内容**:
```
sk-or-v1-xxx  z-ai/glm-5.2
sk-or-v1-yyy  deepseek/deepseek-v4-flash
sk-or-v1-zzz  deepseek/deepseek-v4-pro
```

**柯大侠需要**:
1. 从艾隆处获取 `/tmp/openrouter-key` 文件
2. 或申请自己的 OpenRouter Key: https://openrouter.ai/keys
3. 配置到 channels 表 ID 19 的 `key` 字段

### 6.2 其他渠道 Key
- 硅基流动: 需单独申请 https://cloud.siliconflow.cn/
- 词元: 需单独申请 https://api.tokenriver.cn/
- deepwl: 需单独申请 https://zx1.deepwl.net/

**建议**: 先用 OpenRouter 一个渠道开发测试，其他渠道后续接入

---

## 七、开发任务清单

### 7.1 Phase 1: 基础功能（3-4 天）

**Task 1**: 创建 GLM-5.2 套餐
- [ ] 在 `plans` 表插入 `glm52-basic` / `glm52-pro` / `glm52-flagship`
- [ ] 配置套餐价格、限流参数（5h/日/月）
- [ ] 配置模型白名单（只允许 glm-5.2 相关模型）

**Task 2**: 实现 GLM-5.2 路由逻辑
- [ ] 在 `router.go` 增加 GLM-5.2 分支
- [ ] 实现 Pro 比例控制（basic=0%, pro=10%, flagship=25%）
- [ ] 实现超限静默降级

**Task 3**: 渠道 failover
- [ ] 配置 OpenRouter (ID 19) 为主渠道
- [ ] 配置硅基流动 (ID 18) 为备选
- [ ] 配置智谱官方 (ID 9) 为兜底
- [ ] 实现自动切换逻辑

**Task 4**: 测试验证
- [ ] 创建测试 Token（`glm52-test-alon`）
- [ ] 测试 basic 套餐（纯 Flash）
- [ ] 测试 pro 套餐（Pro≤10%）
- [ ] 测试 flagship 套餐（Pro≤25%）
- [ ] 测试 failover（主渠道故障时自动切换）

### 7.2 Phase 2: 高级功能（后续）

**Task 5**: 成本账本
- [ ] 记录每次调用的实际成本（按 OpenRouter 价格）
- [ ] 计算毛利率（目标 80%+）
- [ ] 生成成本报表

**Task 6**: 智能路由优化
- [ ] 根据渠道实时响应时间动态调整优先级
- [ ] 根据渠道价格动态选择最便宜渠道
- [ ] 实现"聪明模式"（用户可选"速度优先"或"成本优先"）

**Task 7**: 加油包
- [ ] 实现额外流量包购买
- [ ] 支持按量计费（超出套餐部分）

---

## 八、关键文件清单

### 8.1 核心代码
```
internal/
├── api/
│   ├── routes.go              # 路由注册（2115 行）
│   ├── mcp_handler.go         # MCP 工具（create_renewal/query_usage）
│   ├── payment_handler.go     # 支付宝支付
│   └── plan_handler.go        # 套餐管理（新增）
├── model/
│   ├── token.go               # Token 模型
│   ├── channel.go             # 渠道模型
│   └── plan.go                # 套餐模型（新增）
├── service/
│   ├── router.go              # 智能路由（核心）
│   ├── rate_limiter.go        # 限流服务
│   └── cost_calculator.go     # 成本计算
└── config/
    └── config.go              # 配置加载
```

### 8.2 前端页面
```
web/static/
├── token-info.html            # Token 信息查询页
├── cost-dashboard.html        # 成本看板
└── cache-analytics.html       # 缓存分析（新增）
```

### 8.3 配置文件
```
.env                         # 环境变量
internal/config/config.yaml  # 应用配置（如有）
```

---

## 九、测试验证

### 9.1 本地测试
```bash
# 1. 启动服务
go run main.go

# 2. 创建测试 Token
curl -X POST http://localhost:3300/api/v1/tokens \
  -H "Authorization: Bearer your-admin-key" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "glm52-test-alon",
    "plan": "glm52-basic",
    "models": ["glm-5.2"]
  }'

# 3. 测试调用
curl -X POST http://localhost:3300/v1/chat/completions \
  -H "Authorization: Bearer glm52-test-alon" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "glm-5.2",
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

### 9.2 生产验证
**⚠️ 必须建国批准后执行**
```bash
# 1. 同步代码到小龙女/逍遥子
# 2. 编译
go build -o atmapi main.go
# 3. 重启服务
systemctl --user restart atmapi
# 4. 验证健康检查
curl http://localhost:3300/health
```

---

## 十、常见问题

### Q1: 如何切换渠道？
**A**: 修改 `channels` 表的 `priority` 字段，数字越大优先级越高。

### Q2: 如何测试 failover？
**A**: 将主渠道的 `status` 设为 0（禁用），观察是否自动切换到备选渠道。

### Q3: Pro 比例如何统计？
**A**: 查看 `usage_logs` 表，按 `model` 字段分组统计。

### Q4: 如何查看成本？
**A**: 访问 `http://localhost:3300/cost-dashboard`，或调用 `/api/v1/cost-summary`。

### Q5: OpenRouter 促销结束后怎么办？
**A**: 切换到硅基流动（ID 18）或智谱官方（ID 9），修改 `channels` 表的 `priority`。

---

## 十一、联系与支持

**艾隆（准备人）**:
- 飞书: 高建国（建国转发）
- 服务器: 8.220.139.36（艾隆本机）
- 端口: 3300（atmApi 开发环境）

**建国（决策人）**:
- 飞书: 高建国
- 所有生产部署必须建国批准

**柯大侠（开发人）**:
- GitHub: sunecom/atmApi
- 飞书文档: 方案评审在飞书进行

---

## 十二、附录

### 12.1 数据库 schema 文件
**位置**: `/tmp/atmapi-schema.sql`  
**内容**: 10 张表结构（不含数据）  
**用途**: 新建开发库

### 12.2 套餐配置数据
**位置**: `/tmp/atmapi-plans.sql`  
**内容**: 现有套餐列表（不含 Key）  
**用途**: 参考现有套餐结构

### 12.3 模型定价数据
**位置**: `/tmp/atmapi-pricings.sql`  
**内容**: glm-5.2 定价（input=0.008, output=0.028）  
**用途**: 参考定价结构

### 12.4 渠道配置数据
**位置**: 见本文档 4.2 节  
**内容**: 8 个 GLM 渠道配置  
**用途**: 参考渠道结构

---

**文档版本**: v1.0  
**最后更新**: 2026-07-13  
**准备人**: 艾隆
