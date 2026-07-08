package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	// 注册 JPEG/PNG/WebP 解码器
	_ "image/jpeg"
	_ "image/png"
	"log"
	"strings"

	"github.com/disintegration/imaging"
)

// 压缩阈值：base64 超过 500KB 就压缩（原图约 375KB）
const maxImageBase64Bytes = 500 * 1024

// 最大边长（qwen-vl 最佳尺寸 1024px）
const maxImageDimension = 1024

// compressImagesInBody 检查请求体中的图片，超过阈值的压缩
// 支持的格式：
//   1. content 数组 → type: "image_url"｜"image" → 嵌套的图片数据
//   2. content 字符串 → data:image;base64,...
func compressImagesInBody(body []byte) []byte {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}

	messages, ok := req["messages"].([]interface{})
	if !ok {
		return body
	}

	var foundImages, compressedImages int
	modified := false
	for mi, msg := range messages {
		msgMap, ok := msg.(map[string]interface{})
		if !ok {
			continue
		}
		role, _ := msgMap["role"].(string)
		if role != "user" {
			continue
		}

		switch content := msgMap["content"].(type) {
		case []interface{}:
			// 数组格式：[
			//   {type: "image_url", image_url: {url: "..."}},
			//   {type: "text", text: "..."}
			// ]
			for ci, part := range content {
				partMap, ok := part.(map[string]interface{})
				if !ok {
					continue
				}
				typ, _ := partMap["type"].(string)

				// 尝试各种 key 提取图片 URL
				var url string
				switch typ {
				case "image_url":
					foundImages++
					if urlMap, ok := partMap["image_url"].(map[string]interface{}); ok {
						url, _ = urlMap["url"].(string)
					}
				case "image":
					foundImages++
					// type: "image" 可能直接在 part 里带 image_key 或 source 等字段
					if src, ok := partMap["source"].(map[string]interface{}); ok {
						url, _ = src["data"].(string)
					}
					if url == "" {
						url, _ = partMap["image_key"].(string)
					}
				}

				if url == "" || !strings.HasPrefix(url, "data:image") {
					continue
				}
				if len(url) <= maxImageBase64Bytes {
					continue // 小图不压
				}

				newURL, ok := compressDataURLImage(url)
				if !ok {
					continue
				}

				// 更新 part 中的 url
				if typ == "image_url" {
					if urlMap, ok := partMap["image_url"].(map[string]interface{}); ok {
						urlMap["url"] = newURL
						content[ci] = partMap
						compressedImages++
						modified = true
					}
				} else if typ == "image" {
					if src, ok := partMap["source"].(map[string]interface{}); ok {
						src["data"] = newURL
						content[ci] = partMap
						compressedImages++
						modified = true
					}
				}
			}
			if modified {
				msgMap["content"] = content
				messages[mi] = msgMap
			}

		case string:
			// 字符串格式：base64 图片可能嵌入在 content 里
			if strings.Contains(content, "data:image") && len(content) > maxImageBase64Bytes {
				foundImages++
				// 从字符串中提取 base64 部分
				dataIdx := strings.Index(content, "data:image")
				if dataIdx >= 0 {
					log.Printf("[图片压缩] 字符串 content 包含 data:image，位置=%d", dataIdx)
				}
			}
		}
	}

	if !modified {
		if foundImages > 0 {
			log.Printf("[图片压缩] 发现 %d 张图，已压缩 %d 张，%d 张未处理（非 data:image/base64 格式）",
				foundImages, compressedImages, foundImages-compressedImages)
		}
		return body
	}
	req["messages"] = messages
	newBody, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return newBody
}

// compressDataURLImage 把 data:image/...;base64,... 压缩到目标大小以内
// 使用 disintegration/imaging 的 Lanczos 缩放 + 自适应 JPEG 质量
func compressDataURLImage(dataURL string) (string, bool) {
	idx := strings.Index(dataURL, "base64,")
	if idx < 0 {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(dataURL[idx+7:])
	if err != nil {
		return "", false
	}
	origKB := len(raw) / 1024

	// 解码图片（支持 JPEG/PNG/WebP）
	img, err := imaging.Decode(bytes.NewReader(raw), imaging.AutoOrientation(true))
	if err != nil {
		log.Printf("[图片压缩] 解码失败: %v", err)
		return "", false
	}

	// 记录原始尺寸
	bounds := img.Bounds()
	origW, origH := bounds.Dx(), bounds.Dy()

	// Step 1: 如果最大边长超过限制，用 Lanczos 缩放
	var resized image.Image = img
	if origW > maxImageDimension || origH > maxImageDimension {
		resized = imaging.Fit(img, maxImageDimension, maxImageDimension, imaging.Lanczos)
		newW, newH := resized.Bounds().Dx(), resized.Bounds().Dy()
		log.Printf("[图片压缩] Lanczos 缩放: %dx%d → %dx%d", origW, origH, newW, newH)
	}

	// Step 2: 自适应质量 — 逐步降低直到满足阈值
	// 根据原图大小选择起始质量
	startQuality := 85
	if origKB > 1000 {
		startQuality = 75
	}

	for q := startQuality; q >= 40; q -= 10 {
		var buf bytes.Buffer
		if err := imaging.Encode(&buf, resized, imaging.JPEG, imaging.JPEGQuality(q)); err != nil {
			return "", false
		}
		b64 := base64.StdEncoding.EncodeToString(buf.Bytes())
		if len(b64) <= maxImageBase64Bytes {
			newW, newH := resized.Bounds().Dx(), resized.Bounds().Dy()
			log.Printf("[图片压缩] %dKB (%dx%d) → JPEG q%d %dx%d → base64 %dKB (↓%d%%)",
				origKB, origW, origH, q, newW, newH, len(b64)/1024,
				(1-len(b64)*100/(len(raw)*4/3))/1)
			return "data:image/jpeg;base64," + b64, true
		}
		log.Printf("[图片压缩] 质量 %d%% → base64 %dKB (仍超限)", q, len(b64)/1024)
	}

	// Step 3: 极端情况 — 缩到 640px 再试
	if origW > 640 || origH > 640 {
		small := imaging.Fit(img, 640, 640, imaging.Lanczos)
		var buf bytes.Buffer
		imaging.Encode(&buf, small, imaging.JPEG, imaging.JPEGQuality(50))
		b64 := base64.StdEncoding.EncodeToString(buf.Bytes())
		log.Printf("[图片压缩] %dKB → 极限压缩 640px q50 → base64 %dKB", origKB, len(b64)/1024)
		return "data:image/jpeg;base64," + b64, true
	}

	log.Printf("[图片压缩] %dKB → 压不下来，保持原始", origKB)
	return "", false
}

// 供其他文件引用的 fmt（避免 unused）
var _ = fmt.Sprintf
