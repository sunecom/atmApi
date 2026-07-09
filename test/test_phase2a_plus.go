package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const baseURL = "http://localhost:3300"
const testToken = "atm-1778491177466775297"

func main() {
	fmt.Println("=== Phase 2A+ 本地测试 ===\n")

	// 测试 1: 默认行为策略（开发类任务）
	test1()

	// 测试 2: 工具输出压缩
	test2()

	// 测试 3: 咨询类任务不注入默认策略
	test3()

	fmt.Println("\n=== 测试完成 ===")
}

func test1() {
	fmt.Println("【测试 1】默认行为策略 - 开发类任务")
	messages := []map[string]interface{}{
		{"role": "system", "content": "You are a helpful assistant."},
		{"role": "user", "content": "帮我写一个 Python 函数，计算斐波那契数列"},
	}
	resp := sendRequest(messages)
	fmt.Printf("  状态: %d\n", resp.StatusCode)
	fmt.Printf("  实际模型: %s\n", resp.ActualModel)
	fmt.Printf("  回复前100字: %s\n", truncate(resp.Content, 100))
	fmt.Printf("  Token: prompt=%d, completion=%d, total=%d\n\n",
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
}

func test2() {
	fmt.Println("【测试 2】工具输出压缩 - 长工具输出")
	// 模拟一个长工具输出（>2000字）
	longOutput := strings.Repeat("这是工具输出的第 N 行，包含一些日志信息。\n", 100)
	longOutput += "Error: something went wrong at /path/to/file.go:42\n"
	longOutput += "exit code: 1\n"

	messages := []map[string]interface{}{
		{"role": "system", "content": "You are a helpful assistant."},
		{"role": "user", "content": "运行这个命令"},
		{"role": "assistant", "content": "", "tool_calls": []interface{}{
			map[string]interface{}{"id": "call_1", "type": "function", "function": map[string]interface{}{"name": "exec", "arguments": "{}"}},
		}},
		{"role": "tool", "content": longOutput, "tool_call_id": "call_1"},
		{"role": "user", "content": "结果怎么样？"},
	}
	resp := sendRequest(messages)
	fmt.Printf("  状态: %d\n", resp.StatusCode)
	fmt.Printf("  实际模型: %s\n", resp.ActualModel)
	fmt.Printf("  回复前100字: %s\n", truncate(resp.Content, 100))
	fmt.Printf("  Token: prompt=%d, completion=%d, total=%d\n\n",
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
}

func test3() {
	fmt.Println("【测试 3】咨询类任务 - 不注入默认策略")
	messages := []map[string]interface{}{
		{"role": "system", "content": "You are a helpful assistant."},
		{"role": "user", "content": "什么是第一性原理？帮我理解一下"},
	}
	resp := sendRequest(messages)
	fmt.Printf("  状态: %d\n", resp.StatusCode)
	fmt.Printf("  实际模型: %s\n", resp.ActualModel)
	fmt.Printf("  回复前100字: %s\n", truncate(resp.Content, 100))
	fmt.Printf("  Token: prompt=%d, completion=%d, total=%d\n\n",
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
}

type Response struct {
	StatusCode  int
	ActualModel string
	Content     string
	Usage       struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}
}

func sendRequest(messages []map[string]interface{}) Response {
	body := map[string]interface{}{
		"model":    "deepseek-a4",
		"messages": messages,
		"stream":   false,
	}
	jsonBody, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", baseURL+"/v1/chat/completions", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Response{StatusCode: 0, Content: "ERROR: " + err.Error()}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	json.Unmarshal(respBody, &result)

	r := Response{
		StatusCode:  resp.StatusCode,
		ActualModel: resp.Header.Get("X-Actual-Model"),
	}
	if len(result.Choices) > 0 {
		r.Content = result.Choices[0].Message.Content
	}
	r.Usage = result.Usage
	return r
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
