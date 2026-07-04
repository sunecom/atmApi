# atmApi 图片缓存开发文档

> 版本：v1.0 | 2026-07-05
> 目标：为 DALL-E/图片生成请求添加 LRU 缓存，相同 prompt 直接返回缓存图片

---

## 一、现状分析

### 1.1 已有代码

| 文件 | 状态 | 说明 |
|------|------|------|
| `internal/service/image_cache.go` | ✅ 已实现 | LRU 缓存，支持内存+磁盘双层，并发安全 |
| `internal/service/smart_router.go` | ✅ 已使用缓存 | `routeRequest()` 中用 `imageCache.Get()` 判断图片大小做路由 |
| `internal/middleware/ma.go` → `proxyHandler()` | ❌ 未使用缓存 | 核心代理流程，每次请求都转发到上游 |

### 1.2 问题

`proxyHandler()` 处理 `/v1/images/generations` 请求时，**每次都转发到上游 API**，即使完全相同的 prompt 已经生成过图片。

`smart_router.go` 里已经在用 `imageCache.Get(prompt)` 了，但那只用于路由决策，不是真正的缓存命中返回。

### 1.3 结论

**缓存引擎已就绪，只差在代理流程中接入。**

---

## 二、架构设计

### 2.1 缓存位置

```
请求进入
  │
  ▼
proxyHandler()
  │
  ├─ 是图片请求？──否──→ 正常代理流程（不变）
  │
  │ 是
  ▼
生成缓存 key = hash(prompt + size + model + style)
  │
  ▼
imageCache.Get(key)
  │
  ├─ 命中 ──→ 直接构造响应返回（不调上游）
  │
  │ 未命中
  ▼
正常转发到上游 API
  │
  ▼
收到响应 ──→ imageCache.Set(key, response)
  │
  ▼
返回给用户
```

### 2.2 缓存 Key 设计

```go
type ImageCacheKey struct {
    Prompt string `json:"prompt"`
    Size   string `json:"size"`    // 1024x1024, 1792x1024 等
    Model  string `json:"model"`   // dall-e-3, dall-e-2
    Style  string `json:"style"`   // vivid, natural
    N      int    `json:"n"`       // 生成数量
}
```

**为什么包含这些字段**：
- `prompt`：核心内容，不同 prompt 必须不同缓存
- `size`：同一 prompt 不同尺寸是不同图片
- `model`：dall-e-3 和 dall-e-2 生成效果不同
- `style`：vivid 和 natural 风格不同
- `n`：生成数量不同，响应结构不同

### 2.3 缓存 Value

```go
type ImageCacheValue struct {
    Created int64              `json:"created"`      // Unix 时间戳
    Data    []ImageData        `json:"data"`         // 图片数据数组
    RawResp []byte             `json:"-"`            // 原始响应（直接返回）
}

type ImageData struct {
    URL           string `json:"url,omitempty"`
    B64JSON       string `json:"b64_json,omitempty"`
    RevisedPrompt string `json:"revised_prompt,omitempty"`
}
```

---

## 三、实现方案

### 3.1 修改文件清单

| 文件 | 修改内容 |
|------|----------|
| `internal/middleware/ma.go` | 在 `proxyHandler()` 中接入图片缓存 |
| `internal/service/image_cache.go` | 可能需要微调（已实现核心功能） |

### 3.2 proxyHandler 修改逻辑

```go
func proxyHandler(c *gin.Context) {
    // ... 现有代码 ...
    
    // 新增：图片请求缓存检查
    if isImageRequest(c.Request.URL.Path) {
        cacheKey := buildImageCacheKey(reqBody)
        
        // 尝试从缓存获取
        if cached, found := service.ImageCache.Get(cacheKey); found {
            log.Printf("[ImageCache] HIT: prompt=%s", reqBody.Prompt)
            c.Data(http.StatusOK, "application/json", cached.RawResp)
            return
        }
        
        log.Printf("[ImageCache] MISS: prompt=%s", reqBody.Prompt)
    }
    
    // ... 正常转发到上游 ...
    
    // 新增：缓存响应
    if isImageRequest(c.Request.URL.Path) && resp.StatusCode == 200 {
        cacheKey := buildImageCacheKey(reqBody)
        service.ImageCache.Set(cacheKey, respBody)
    }
}
```

### 3.3 辅助函数

```go
func isImageRequest(path string) bool {
    return strings.Contains(path, "/images/generations")
}

func buildImageCacheKey(req *ImageRequest) string {
    key := struct {
        Prompt string `json:"p"`
        Size   string `json:"s"`
        Model  string `json:"m"`
        Style  string `json:"st"`
        N      int    `json:"n"`
    }{
        Prompt: req.Prompt,
        Size:   req.Size,
        Model:  req.Model,
        Style:  req.Style,
        N:      req.N,
    }
    data, _ := json.Marshal(key)
    hash := sha256.Sum256(data)
    return fmt.Sprintf("img:%x", hash[:8])
}
```

---

## 四、配置项

### 4.1 环境变量

```bash
# 图片缓存配置
IMAGE_CACHE_ENABLED=true        # 是否启用图片缓存
IMAGE_CACHE_MAX_ITEMS=100       # 最大缓存图片数量
IMAGE_CACHE_TTL_HOURS=24        # 缓存过期时间（小时）
IMAGE_CACHE_DISK_PATH=./data/image_cache  # 磁盘缓存目录
IMAGE_CACHE_DISK_MAX_MB=1024    # 磁盘缓存