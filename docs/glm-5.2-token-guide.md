# GLM-5.2 专属 Token 配置指南

> **创建时间**: 2026-06-26  
> **负责人**: 艾隆 (Elon)  
> **Token ID**: 8  
> **配额**: 1000 次 / 5小时（自动重置）

---

## 📋 基本信息

| 项目 | 值 |
|------|-----|
| **Token 名称** | `glm-5.2-exclusive` |
| **Token ID** | 8 |
| **Token 前缀** | `sk-glm52-` |
| **完整 Token** | `sk-glm52-<随机后缀>`（需从数据库查询） |
| **配额限制** | 1000 次 / 5小时 |
| **自动重置** | ✅ 每5小时重置为 1000 次 |
| **适用模型** | `glm-5.2`（智谱 GLM-5.2） |
| **API 地址** | `https://atmapi.aitomoney.online/v1` |

---

## 🚀 快速使用

### OpenClaw 配置
```yaml
llm:
  provider: openai
  baseURL: https://atmapi.aitomoney.online/v1
  apiKey: "sk-glm52-<完整token>"
  model: glm-5.2
```

### Python 示例（OpenAI SDK）
```python
from openai import OpenAI

client = OpenAI(
    api_key="sk-glm52-<完整token>",
    base_url="https://atmapi.aitomoney.online/v1"
)

response = client.chat.completions.create(
    model="glm-5.2",
    messages=[{"role": "user", "content": "你好"}]
)

print(response.choices[0].message.content)
```

### cURL 测试
```bash
curl -X POST https://atmapi.aitomoney.online/v1/chat/completions \
  -H "Authorization: Bearer sk-glm52-<完整token>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "glm-5.2",
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

---

## 📊 配额管理

| 参数 | 值 | 说明 |
|------|-----|------|
| **初始配额** | 1000 次 | 每次重置后的可用次数 |
| **重置周期** | 5 小时 | 每 5 小时自动重置 |
| **超额处理** | 返回 429 错误 | 等待下次重置即可 |
| **自动重置任务** | cron: `0 */5 * * *` | OpenClaw cron 管理 |

### 手动查询配额
```bash
sqlite3 ~/.openclaw/workspace/atmApi/data/atmapi.db \
  "SELECT name, remain_quota FROM tokens WHERE name='glm-5.2-exclusive';"
```

### 手动重置配额
```bash
sqlite3 ~/.openclaw/workspace/atmApi/data/atmapi.db \
  "UPDATE tokens SET remain_quota=1000 WHERE name='glm-5.2-exclusive';"
```

---

## ⚠️ 注意事项

1. **Token 安全**: 请妥善保管 Token，勿泄露给他人
2. **模型限制**: atmApi 当前不支持 Token 级别的模型绑定，该 Token 仍可调用其他渠道（需代码层限制）
3. **配额监控**: 达到限额后返回 429 错误，等待 5 小时后自动恢复
4. **API 地址**: 推荐使用 HTTPS 域名 `https://atmapi.aitomoney.online/v1`，无需指定端口

---

## 🔧 技术细节

### 数据库配置
```sql
-- 查看 Token 信息
SELECT id, name, key, remain_quota, unlimited_quota 
FROM tokens 
WHERE name='glm-5.2-exclusive';

-- 结果示例:
-- id: 8
-- name: glm-5.2-exclusive
-- key: sk-glm52-<随机后缀>
-- remain_quota: 1000
-- unlimited_quota: 0
```

### 渠道配置
```sql
-- GLM-5.2 渠道信息
SELECT id, name, type, models 
FROM channels 
WHERE models LIKE '%glm-5.2%';

-- 结果:
-- id: 7
-- name: 智谱 GLM-5.2 (cmkey.cn)
-- type: 14
-- models: glm-5.2
```

### 自动重置 Cron 任务
- **任务名称**: `glm-5.2 Token 配额重置`
- **执行时间**: 每 5 小时（cron: `0 */5 * * *`）
- **执行内容**: 将 `remain_quota` 重置为 1000

---

## 📝 更新日志

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-06-26 | 创建 Token | ID: 8，初始配额 1500 次/5小时 |
| 2026-06-26 | 修改配额 | 从 1500 次改为 1000 次/5小时 |
| 2026-06-26 | 验证 Token | ✅ API 调用正常，GLM-5.2 响应成功 |

---

**最后更新**: 2026-06-26  
**维护者**: 艾隆 (Elon)