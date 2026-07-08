# atmApi 使用说明 — AI API 智能网关

> 一个 Token，接入多个 AI 模型。智能路由，按量计费。

---

## 📦 产品线

atmApi 提供三条独立产品线，套餐互不通用：

| 产品线 | 定位 | 适用场景 |
| :--- | :--- | :--- |
| **DeepSeek-A4** | 智能路由模型 | 个人/团队日常对话 |
| **OpenClaw（atm卡）** | OpenClaw Gateway 专属 | 智能体持续运行 |
| **Hermes（小马哥）** | 自动化工作流专用 | 爬虫、数据分析、批量处理 |

---

## 🧠 DeepSeek-A4 — 智能路由模型

`deepseek-a4` 不是单一模型，是一个智能路由层：

| 输入类型 | 自动路由到 | 特点 |
| :--- | :--- | :--- |
| 简单文本 | deepseek-v4-flash | 快速便宜 |
| 复杂文本 | deepseek-v4-pro | 深度推理，1M 上下文 |
| 含图片 | qwen3.7-plus | 多模态视觉 |

### 套餐

| 套餐 | 价格 | 5小时 | 每日 | 每月 | 图片/天 | RPM |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| 基础版 | ¥29.9/月 | 100 | 400 | 8,000 | — | 10 |
| 专业版 | ¥69/月 | 300 | 1,200 | 24,000 | 50 | 30 |
| 创业版 | ¥99/月 | 400 | 1,600 | 30,000 | 100 | — |
| 旗舰版 | ¥129/月 | 600 | 2,400 | 50,000 | 200 | 60 |
| 高级版 | ¥299/月 | 1,200 | 4,800 | 100,000 | 500 | — |
| 企业版 | ¥599/月 | 3,000 | 12,000 | 300,000 | 2,000 | — |

---

## 🔧 OpenClaw 套餐（atm卡）

专为 OpenClaw Gateway 用户设计，不限图片次数，适合智能体持续对话。

### 套餐

| 套餐 | 价格 | 5小时 | 每日 | 每月 | 并发 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| OpenClaw 基础 | ¥99/月 | 500 | 2,000 | 30,000 | 20 |
| OpenClaw 进阶 | ¥299/月 | 2,000 | 8,000 | 100,000 | 80 |

---

## 🤖 Hermes 套餐（小马哥）

专为自动化工作流设计，高并发、高配额。

### 套餐

| 套餐 | 价格 | 5小时 | 每日 | 每月 | 并发 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| Hermes 基础 | ¥149/月 | 800 | 3,200 | 50,000 | 40 |
| Hermes 进阶 | ¥399/月 | 3,000 | 12,000 | 150,000 | 120 |

---

## 🔑 通用接入信息

- **API 地址**：`https://atmapi.aitomoney.online/v1`
- **API Key**：购买后发放（以 sk-dp- 开头）
- **模型名称**：`deepseek-a4`（Hermes 由工作流自动指定）

---

## 🚀 快速开始

### OpenClaw Gateway（推荐）

```json
{
  "providers": {
    "atmapi": {
      "baseUrl": "https://atmapi.aitomoney.online/v1",
      "apiKey": "***"
    }
  },
  "model": { "primary": "atmapi/deepseek-a4" }
}
```

### Python

```python
from openai import OpenAI
client = OpenAI(api_key="***", base_url="https://atmapi.aitomoney.online/v1")
response = client.chat.completions.create(
    model="deepseek-a4",
    messages=[{"role": "user", "content": "你好！"}]
)
print(response.choices[0].message.content)
```

### cURL

```bash
curl -X POST https://atmapi.aitomoney.online/v1/chat/completions \
  -H "Authorization: Bearer 您的Key" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-a4","messages":[{"role":"user","content":"你好"}]}'
```

---

## 📈 用量查询

- **Token 查询页面**：https://atmapi.aitomoney.online/token-info（输入 Key 查看配额、套餐、到期时间）
- **对话中查询**：发送「查询用量」，系统自动返回

---

## 🔌 MCP 集成

```json
{
  "mcpServers": {
    "atmapi": {
      "type": "http",
      "url": "https://atmapi.aitomoney.online/mcp",
      "headers": { "Authorization": "***" }
    }
  }
}
```

**可用工具**：`query_usage`（查询用量）、`create_renewal`（续费/升级）、`list_models`（查看套餐）

---

## ❓ 常见问题

**Q: 不同产品线的套餐能混用吗？**
A: 不能。DeepSeek-A4、OpenClaw、Hermes 是独立产品线。

**Q: 配额用完了怎么办？**
A: 滑动窗口自动重置，月卡有效期内持续有效。

**Q: Token 会过期吗？**
A: 激活后 30 天有效，到期前 7 天自动提醒。

**Q: 如何续费或升级？**
A: 对话中发送「续费」或访问 token-info 页面。

---

📞 售后：OpenClaw 对话中直接提问，或访问 https://atmapi.aitomoney.online/token-info
