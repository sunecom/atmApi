# GLM-5.2 API 服务 — 使用说明

感谢您的购买！以下是您的专属 API 接口信息。

---

## 🔑 您的专属信息

- **API 地址**：`https://atmapi.aitomoney.online/v1`
- **API Key**：`<这里替换为客户的 Token>`
- **模型名称**：`glm-5.2`
- **有效期**：30 天（自购买之日起）

## 📊 您的套餐

| 套餐 | 配额 | 重置周期 |
| :--- | :--- | :--- |
| 入门版 | 500 次 | 每 5 小时自动重置 |
| 专业版 | 1000 次 | 每 5 小时自动重置 |
| 旗舰版 | 1500 次 | 每 5 小时自动重置 |

---

## 🚀 快速开始

### 方式一：常见客户端（ChatBox、NextChat 等）

1. 打开客户端设置，选择「自定义 API」
2. **API 地址**填入：`https://atmapi.aitomoney.online/v1`
3. **API Key**填入您的专属 Key
4. **模型**填入：`glm-5.2`
5. 保存即可开始对话！

### 方式二：Python 调用

```python
from openai import OpenAI

client = OpenAI(
    api_key="您的专属Key",
    base_url="https://atmapi.aitomoney.online/v1"
)

response = client.chat.completions.create(
    model="glm-5.2",
    messages=[{"role": "user", "content": "你好！"}]
)

print(response.choices[0].message.content)
```

### 方式三：cURL 测试

```bash
curl -X POST https://atmapi.aitomoney.online/v1/chat/completions \
  -H "Authorization: Bearer 您的专属Key" \
  -H "Content-Type: application/json" \
  -d '{"model":"glm-5.2","messages":[{"role":"user","content":"你好"}]}'
```

---

## ❓ 常见问题

**Q：支持哪些客户端？**
A：所有兼容 OpenAI API 格式的客户端均可使用，如 ChatBox、NextChat、LobeChat、Open WebUI 等。

**Q：配额用完了怎么办？**
A：每 5 小时自动重置，无需操作。等待重置后即可恢复使用。

**Q：到期后怎么续费？**
A：请在淘宝店铺重新拍下对应套餐，我们将为您发放新的 Key。

**Q：响应速度慢怎么办？**
A：网络波动可能导致偶尔变慢，通常会自动恢复。如持续异常请联系客服。

---

## 📞 售后联系

如有任何问题，请在淘宝订单中联系客服。
