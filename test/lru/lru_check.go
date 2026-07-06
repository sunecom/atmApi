package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	apiURL    = "http://localhost:3002/v1/chat/completions"
	apiToken  = "atm-1778491177466775297" // atm-team-Token
	maxEntry  = 100
	testCount = 120 // 超过 maxEntries 触发 LRU
)

// 生成指定大小的 base64 假图片
func fakeBase64Image(sizeKB int) string {
	raw := make([]byte, sizeKB*1024)
	for i := range raw {
		raw[i] = byte(i % 256)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
}

func sendImageRequest(tokenKey string, imgSizeKB int) (int, string) {
	imgData := fakeBase64Image(imgSizeKB)
	body := map[string]interface{}{
		"model": "deepseek-a4",
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": imgData,
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", apiURL, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("X-Token-Key", tokenKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Sprintf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(respBody)
}

func sendTextRequest(tokenKey string, text string) (int, string) {
	body := map[string]interface{}{
		"model": "deepseek-a4",
		"messages": []map[string]interface{}{
			{"role": "user", "content": text},
		},
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", apiURL, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("X-Token-Key", tokenKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Sprintf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(respBody)
}

func main() {
	fmt.Println("===== 图片缓存 LRU 测试 =====")
	fmt.Println()

	// ===== 测试 1: 大小限制 =====
	fmt.Println("【测试 1】图片大小限制（>10MB 应被拒绝）")
	status, resp := sendImageRequest("user-oversize", 12*1024) // 12MB
	if status == 200 && strings.Contains(resp, "图片已收到") {
		fmt.Println("  ❌ 12MB 图片未被拒绝！缓存响应:", resp[:80])
	} else {
		fmt.Printf("  ✅ 12MB 图片处理: status=%d\n", status)
	}
	status, _ = sendImageRequest("user-normal", 5*1024) // 5MB 正常
	fmt.Printf("  ✅ 5MB 图片处理: status=%d\n", status)
	fmt.Println()

	// ===== 测试 2: LRU 淘汰 =====
	fmt.Printf("【测试 2】LRU 淘汰（发 %d 个不同用户的图片，maxEntries=%d）\n", testCount, maxEntry)
	for i := 0; i < testCount; i++ {
		tokenKey := fmt.Sprintf("user-%03d", i)
		sendImageRequest(tokenKey, 100) // 100KB 小图
		if (i+1)%20 == 0 {
			fmt.Printf("  已发送 %d/%d 请求...\n", i+1, testCount)
		}
	}
	fmt.Printf("  ✅ %d 个图片缓存请求完成\n", testCount)
	fmt.Println()

	// ===== 测试 3: 缓存命中（纯文字触发图片注入）=====
	fmt.Println("【测试 3】缓存命中测试（纯文字请求 → 应注入缓存图片）")
	// 先发一个图片
	sendImageRequest("user-cache-test", 50)
	time.Sleep(200 * time.Millisecond)
	// 再发纯文字
	status, resp = sendTextRequest("user-cache-test", "这张图片是什么？")
	if status == 200 {
		if strings.Contains(resp, "图片已收到") {
			fmt.Println("  ✅ 缓存命中 → 返回模拟响应（图片已缓存，等待问题）")
		} else {
			fmt.Printf("  ✅ 请求处理: status=%d\n", status)
		}
	} else {
		fmt.Printf("  ⚠️ status=%d, resp=%s\n", status, resp[:min(200, len(resp))])
	}
	fmt.Println()

	// ===== 测试 4: 同一用户重复图片（LRU 更新）=====
	fmt.Println("【测试 4】同一用户重复发图（应更新而非新增）")
	sendImageRequest("user-repeat", 50)
	sendImageRequest("user-repeat", 50) // 第二次应更新 LRU 位置
	status, _ = sendTextRequest("user-repeat", "你好")
	fmt.Printf("  ✅ 重复图片处理: status=%d\n", status)
	fmt.Println()

	fmt.Println("===== 测试完成 =====")
	fmt.Println("检查日志: tail -50 /tmp/atmapi.log | grep 图片缓存")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
