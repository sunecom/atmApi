# 客户使用指南模板

> 用于发送给购买 API 服务的客户，可根据不同产品线调整配额说明部分。

---

## 通用模板（适合所有月卡类）

```
🚀 GLM-5.2 专属 API 接入指南

━━━━━━━━━━━━━━━━━━━━

🔑 您的专属 Token

{完整Token}

请妥善保管，勿泄露给他人。

━━━━━━━━━━━━━━━━━━━━

🔍 查询额度 & 激活状态

https://atmapi.aitomoney.online/token-info

输入您的 Token 即可实时查看剩余额度和使用记录。

━━━━━━━━━━━━━━━━━━━━

🌐 接入地址

https://atmapi.aitomoney.online/api/v1

━━━━━━━━━━━━━━━━━━━━

⚙️ 使用示例

【Python（OpenAI 兼容格式）】

from openai import OpenAI

client = OpenAI(
    api_key="{您的Token}",
    base_url="https://atmapi.aitomoney.online/api/v1"
)

response = client.chat.completions.create(
    model="glm-5.2",
    messages=[{"role": "user", "content": "你好"}]
)
print(response.choices[0].message.content)

【JavaScript（Fetch）】

const response = await fetch('https://atmapi.aitomoney.online/api/v1/chat/completions', {
    method: 'POST',
    headers: {
        'Content-Type': 'application/json',
        'Authorization': 'Bearer {您的Token}'
    },
    body: JSON.stringify({
        model: 'glm-5.2',
        messages: [{role: 'user', content: '你好'}]
    })
});
const data = await response.json();
console.log(data.choices[0].message.content);

【cURL】

curl -X POST https://atmapi.aitomoney.online/api/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer {您的Token}" \
  -d '{"model":"glm-5.2","messages":[{"role":"user","content":"你好"}]}'

━━━━━━━━━━━━━━━━━━━━

📊 配额说明

{根据产品线填写，见下方}

━━━━━━━━━━━━━━━━━━━━

💡 注意事项

1. 请妥善保管 Token，勿泄露给他人
2. 本 Token 专用于 glm-5.2 模型
3. Token 首次使用后 30 天有效，请在有效期内使用
4. 如有技术问题，请联系客服

📖 完整使用文档：https://atmapi.aitomoney.online/help
```

---

## 各产品线配额说明（替换模板中的 📊 配额说明 部分）

### 9.9元体验版

```
📊 配额说明

总调用次数：100 次
使用方式：一次性额度，用完即止
有效期：首次使用后 30 天
超额提示：额度用完后返回 429 错误，需购买月卡继续使用
```

### 性价比月卡（¥9.9）

```
📊 配额说明

调用限额：500 次 / 5小时
每周上限：40000 次 / 周
自动重置：每 5 小时自动恢复额度
超额提示：达到限额后返回 429 错误，等待下次重置即可
有效期：首次使用后 30 天
```

### 基础版月卡（¥29.9）

```
📊 配额说明

调用限额：1000 次 / 5小时
每周上限：40000 次 / 周
自动重置：每 5 小时自动恢复额度
超额提示：达到限额后返回 429 错误，等待下次重置即可
有效期：首次使用后 30 天
```

### 升级版月卡（¥49.9）

```
📊 配额说明

调用限额：1500 次 / 5小时
每周上限：40000 次 / 周
自动重置：每 5 小时自动恢复额度
超额提示：达到限额后返回 429 错误，等待下次重置即可
有效期：首次使用后 30 天
```

### 黄金月卡（¥99.9）

```
📊 配额说明

调用限额：2000 次 / 5小时
每周上限：40000 次 / 周
自动重置：每 5 小时自动恢复额度
超额提示：达到限额后返回 429 错误，等待下次重置即可
有效期：首次使用后 30 天
```

### 大胃王月卡（¥199.9）

```
📊 配额说明

5小时限额：不限次数
每周上限：40000 次 / 周
自动重置：每周一 00:00 重置周限额
超额提示：达到周限额后返回 429 错误，等待下周一重置即可
有效期：首次使用后 30 天
```

---

## 快速发送示例（性价比月卡）

```
🚀 GLM-5.2 专属 API 接入指南

━━━━━━━━━━━━━━━━━━━━

🔑 您的专属 Token

sk-glm-v1-xxxxxxxxxxxx

请妥善保管，勿泄露给他人。

━━━━━━━━━━━━━━━━━━━━

🔍 查询额度 & 激活状态

https://atmapi.aitomoney.online/token-info

输入您的 Token 即可实时查看剩余额度和使用记录。

━━━━━━━━━━━━━━━━━━━━

🌐 接入地址

https://atmapi.aitomoney.online/api/v1

━━━━━━━━━━━━━━━━━━━━

⚙️ 使用示例

【Python（OpenAI 兼容格式）】

from openai import OpenAI

client = OpenAI(
    api_key="sk-glm-v1-xxxxxxxxxxxx",
    base_url="https://atmapi.aitomoney.online/api/v1"
)

response = client.chat.completions.create(
    model="glm-5.2",
    messages=[{"role": "user", "content": "你好"}]
)
print(response.choices