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

// compressImagesInBody 检查请求体中的 base64 图片，超过阈值的压缩
func compressImagesInBody(body []byte) []byte {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}

	messages, ok := req["messages"].([]interface{})
	if !ok {
		return body
	}

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
		content, ok := msgMap["content"].([]interface{})
		if !ok {
			continue
		}
		for ci, part := range content {
			partMap, ok := part.(map[string]interface{})
			if !ok {
				continue
			}
			typ, _ := partMap["type"].(string)
			if typ != "image_url" {
				continue
			}
			urlMap, ok := partMap["image_url"].(map[string]interface{})
			if !ok {
				continue
			}
			url, ok := urlMap["url"].(string)
			if !ok || !strings.HasPrefix(url, "data:image") {
				continue
			}
			if len(url) <= maxImageBase64Bytes {
				continue // 小图不压
			}

			newURL, ok := compressDataURLImage(url)
			if !ok {
				continue
			}
			urlMap["url"] = newURL
			content[ci] = partMap
			modified = true
		}
		if modified {
			msgMap["content"] = content
			messages[mi] = msgMap
		}
	}

	if !modified {
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
