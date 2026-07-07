# 图片分析缓存方案 v2（Image Analysis Cache）

> 创建时间：2026-07-06
> 更新时间：2026-07-07（v2 — 修正跨服务器图片提取问题）
> 作者：艾隆（Elon）+ 建国
> 状态：实现中

## 核心理念

**分析结果替代原图**——用户发图后，异步转发请求给 Qwen 分析，用文字描述替换后续请求中的图片。上下文从 MB 降到 KB。

## 关键洞察（v1 → v2 的修正）

v1 的错误：试图在 atmapi 服务器上提取图片 bytes（base64/file:///），然后自己调 Qwen。
v2 的修正：atmapi 不需要提取图片，只需要**原封不动把完整请求转发给 Qwen**。

```
为什么能 work：
- OpenClaw 发给 atmapi 的请求已经包含图片信息
- atmapi 把请求转发给 Qwen → Qwen 分析 → 返回结果
- 这个流程一直在 work（ID 318-322 证明 Qwen 能成功分析）
- v2 只是把这个过程异步化
```

## 数据流（v2）

```
用户发图 → OpenClaw 构造请求（含图片）→ atmapi 收到
  ↓
Step 1: HasImageContent？（只看最后一条 user 消息）
  ├─ YES → Step 2
  └─ NO  → Step 5（纯文字请求）
  ↓
Step 2: 有实质性文字问题？
  ├─ 无（纯图）→ Step 3
  └─ 有（图+文字）→ 正常路由 Qwen 分析
  ↓
Step 3: 立即返回"图片已收到，你需要我帮你做什么？"（不阻塞）
  ↓
Step 4: 异步 — 把原始请求（含图片的完整 messages）转发给 Qwen
  → Qwen 分析图片 → 返回文字描述
  → 缓存：消息 hash → 文字描述（30 分钟 TTL）
  ↓
  --- 用户发文字追问 ---
  ↓
Step 5: 遍历历史消息
  → 遇到图片消息 → 计算 hash → 查缓存
  → 有缓存 → 替换为文字描述
  → 无缓存 → 保留原始消息
  ↓
Step 6: SmartRoute（此时消息中可能已无图）→ DeepSeek Flash/Pro
  ↓
Step 7: 转发 DeepSeek → 快速响应（1-2s）
```

## 与 v1 的关键区别

| 维度 | v1（废弃） | v2（当前） |
|------|-----------|-----------|
| 图片提取 | ExtractAndHash 提取 bytes | 不提取，直接转发原始 messages |
| 分析调用 | 自己构造 base64 请求 | 把 OpenClaw 原始请求直接转发 |
| 跨服务器 | ❌ file:/// 读不到 | ✅ 不需要读文件 |
| 缓存 key | SHA256(image_bytes) | SHA256(原始 messages 的 JSON) |

## 缓存设计

```go
type ImageAnalysisCache struct {
    mu      sync.RWMutex
    items   map[string]*AnalysisEntry   // key: 消息 hash
    pending map[string]bool             // 正在分析中
    notify  map[string]chan bool        // 分析完成通知
    ttl     time.Duration               // 30 分钟
}

type AnalysisEntry struct {
    Description string    // Qwen 的分析文本
    AnalyzedAt  time.Time
}
```

### AnalyzeAsync v2

```go
// 不提取图片 bytes，直接转发原始 messages 给 Qwen
func (c *ImageAnalysisCache) AnalyzeAsync(hash string, messages []map[string]interface{}) {
    go func() {
        // 构造请求：用原始 messages + 分析 prompt
        reqMessages := append(messages, map[string]interface{}{
            "role": "user",
            "content": "请详细描述这张图片中的所有内容...",
        })
        
        // 调用 Qwen（通过现有 RouteRequest）
        result, err := RouteRequest("qwen3.7-plus", body, "")
        // 存缓存
        c.items[hash] = &AnalysisEntry{desc, time.Now()}
    }()
}
```

## 上下文体积对比

```
3张图 + 3轮对话：

旧方案（原图注入）：
  请求1: 2MB → Qwen 8s
  请求2: 4MB → Qwen 15s
  请求3: 6MB → Qwen 30s+
  请求4: 8MB → Qwen 502 ❌

v2 方案（文字替换）：
  请求1: 图1分析(~2KB) → DeepSeek 1.5s
  请求2: 图1+2分析(~4KB) → DeepSeek 1.5s
  请求3: 图1+2+3分析(~6KB) → DeepSeek 1.8s
  请求4: 图1+2+3分析+对话(~10KB) → DeepSeek 2s ✅
```

## 边界处理

| 情况 | 处理 |
|------|------|
| 用户秒回 | 等 3 秒再查缓存；没完成用占位文本 |
| 同一请求重复 | hash 去重 |
| Qwen 分析失败 | 缓存空 → 后续走正常 Qwen 分析（不影响） |
| 分析超时(30s) | 释放 pending → 后续走正常路由 |

## 实现清单

1. ✅ `image_analysis.go` — 已有缓存框架
2. 🔧 修改 `AnalyzeAsync` — 改为转发原始 messages（不再提取 bytes）
3. 🔧 修改 `routes.go` — 调用 `AnalyzeAsync(hash, messages)` 而非 `AnalyzeAsync(hash, imgBytes)`
4. 🔧 修改 `ReplaceImagesWithText` — 用消息 hash 而非图片 bytes hash
5. 🧪 测试验证
