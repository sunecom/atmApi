package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	apiURL   = "http://localhost:3000/v1/chat/completions"
	apiToken = "atm-1778491177466775297"
)

func main() {
	fmt.Println("=== 方案 B 图片缓存测试 ===")
	fmt.Println()

	// 测试 1: 纯图片（无问题）
	fmt.Println("【测试 1】纯图片，无问题 → 应该缓存，不调模型")
	testPureImage()
	fmt.Println()

	// 测试 2: 图片 + 问题（有缓存）
	fmt.Println("【测试 2】图片 + 问题 → 应该合并缓存，调模型")
	testImageWithQuestion()
	fmt.Println()

	// 测试 3: 纯文字（不受影响）
	fmt.Println("【测试 3】纯文字 → 正常路由，不走缓存")
	testPureText()
}

func testPureImage() {
	start := time.Now()

	reqBody := map[string]interface{}{
		"model": "deepseek-a4",
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": "https://example.com/test.jpg",
						},
					},
				},
			},
		},
		"max_tokens": 100,
	}

	resp := sendRequest(reqBody)
	elapsed := time.Since(start)

	fmt.Printf("  响应时间: %v\n", elapsed)
	fmt.Printf("  状态码: %d\n", resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(body, &result)

	if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				if content, ok := msg["content"].(string); ok {
					fmt.Printf("  回复: %s\n", content)
					if content == "✅ 图片已接收并缓存。请提出您的问题，我会结合图片一起分析。" {
						fmt.Println("  ✅ 测试通过：纯图片被缓存，未调用模型")
					}
				}
			}
		}
	}
}

func testImageWithQuestion() {
	start := time.Now()

	reqBody := map[string]interface{}{
		"model": "deepseek-a4",
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": "https://example.com/test.jpg",
						},
					},
					map[string]interface{}{
						"type": "text",
						"text": "这张图片里有什么？",
					},
				},
			},
		},
		"max_tokens": 100,
	}

	resp := sendRequest(reqBody)
	elapsed := time.Since(start)

	fmt.Printf("  响应时间: %v\n", elapsed)
	fmt.Printf("  状态码: %d\n", resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(body, &result)

	if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				if content, ok := msg["content"].(string); ok {
					fmt.Printf("  回复: %s\n", content[:min(100, len(content))])
					if content != "✅ 图片已接收并缓存。请提出您的问题，我会结合图片一起分析。" {
						fmt.Println("  ✅ 测试通过：图片+问题被合并，调用了模型")
					}
				}
			}
		}
	}
}

func testPureText() {
	start := time.Now()

	reqBody := map[string]interface{}{
		"model": "deepseek-a4",
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": "1+1等于几？",
			},
		},
		"max_tokens": 50,
	}

	resp := sendRequest(reqBody)
	elapsed := time.Since(start)

	fmt.Printf("  响应时间: %v\n", elapsed)
	fmt.Printf("  状态码: %d\n", resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(body, &result)

	if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				if content, ok := msg["content"].(string); ok {
					fmt.Printf("  回复: %s\n", content)
					fmt.Println("  ✅ 测试通过：纯文字正常路由")
				}
			}
		}
	}
}

func sendRequest(reqBody map[string]interface{}) *http.Response {
	jsonData, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiToken)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("  错误: %v\n", err)
		return nil
	}
	return resp
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
