# atmApi 使用指南

## 快速入门

### 1. 登录后台

访问 `http://localhost:3002`，使用默认账号登录：

| 用户名 | 密码 |
|-------|------|
| admin | admin123 |

### 2. 添加上游渠道

1. 进入「渠道管理」
2. 填写渠道信息：
   - **名称**：示例"通义千问"
   - **Base URL**：上游 API 地址
   - **API Key**：上游的 API 密钥
   - **模型**：支持的模型，逗号分隔，例如 `qwen3.5-plus,qwen-max`
   - **优先级**：数值越高越优先（主渠道设 10，备用设 5）
3. 点击创建

### 3. 创建 API Token

1. 进入「Token 管理」
2. 填写 Token 名称
3. 设置配额（-1=无限，或指定可用次数）
4. 点击创建
5. 复制生成的 Token key（格式：`atm-xxxx`）

### 4. 测试调用

```bash
curl -X POST http://localhost:3002/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer atm-xxxx" \
  -d '{
    "model": "qwen3.5-plus",
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

### 5. 配置 Fallback（自动切换）

**场景**：主渠道不可用时（4xx/5xx/超时），自动切到备用渠道。

**配置方法**：
1. 创建渠道 A（主）：优先级 10，权重 10
2. 创建渠道 B（备用）：优先级 5，权重 10
3. 系统自动按优先级选择，同优先级按权重负载均衡

### 6. 模型映射

如果上游模型名和请求模型名不一致，可配置映射：

```json
{
  "qwen3.5-plus": "deepseek-v4-flash",
  "gpt-4": "qwen-turbo"
}
```

映射在「新增渠道」的「模型映射 JSON」字段中设置。

### 7. 查看请求日志

1. 进入「请求日志」
2. 查看所有 API 调用记录
3. 包含：时间、Token、渠道、模型、状态码、响应耗时

---

## API 接口文档

### 管理接口（需 JWT 认证）

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/v1/login | 登录 |
| POST | /api/v1/register | 注册 |
| GET | /api/v1/channels | 获取渠道列表 |
| POST | /api/v1/channels | 创建渠道 |
| PUT | /api/v1/channels/:id | 更新渠道 |
| DELETE | /api/v1/channels/:id | 删除渠道 |
| GET | /api/v1/tokens | 获取 Token 列表 |
| POST | /api/v1/tokens | 创建 Token |
| PUT | /api/v1/tokens/:id | 更新 Token |
| DELETE | /api/v1/tokens/:id | 删除 Token |
| GET | /api/v1/logs | 获取请求日志 |
| GET | /api/v1/models | 获取可用模型列表 |

### 调用接口（需 API Token）

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/v1/chat/completions | OpenAI 兼容的聊天接口 |

### 公开接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | / | 管理后台首页 |
| GET | /health | 健康检查 |

---

## 部署建议

### 生产环境
```bash
# 使用 systemd 管理服务
cat > /etc/systemd/system/atmapi.service <<EOF
[Unit]
Description=atmApi Service
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/atmapi -port 3002
Restart=always
RestartSec=10
WorkingDirectory=/opt/atmapi

[Install]
WantedBy=multi-user.target
EOF

systemctl enable atmapi
systemctl start atmapi
```

### 安全建议
1. 修改默认密码
2. 配置 JWT_SECRET 环境变量
3. 如公网访问，配置 HTTPS 反向代理
