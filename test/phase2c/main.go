package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const baseURL = "http://localhost:3300"
const testToken = "atm-1778491177466775297"

func main() {
	fmt.Print("...")

	// 测试 1: 安全补丁 - 用户要求分步确认
	test1()

	// 测试 2: 安全补丁 - 用户要求一步步来
	test2()

	// 测试 3: 任务模式路由 - 调试类任务
	test3()

	// 测试 4: 任务模式路由 - 闲聊类
	test4()

	// 测试 5: 成本仪表盘 API（需要 JWT）
	test5()

	// 测试 6: 单 token 成本 API（需要 JWT）
	test6()

	fmt.Println("=== Phase 2C 测试完成 ===")
}

func test1() {
	fmt.Println("【测试 1】安全补丁 - 用户要求分步确认")
	messages := []map[string]interface{}{
		{"role": "system", "content": "You are a helpful assistant."},
		{"role": "user", "content": "帮我重构这个项目，分步骤问我确认"},
	}
	resp := sendRequest(messages)
	fmt.Printf("  状态: %d\n", resp.StatusCode)
	fmt.Printf("  实际模型: %s\n", resp.ActualModel)
	fmt.Printf("  回复前150字: %s\n", truncate(resp.Content, 150))
	fmt.Printf("  Token: prompt=%d, completion=%d, total=%d\n",
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	fmt.Println("  预期: 模型应该先询问确认，而不是一次性完成")
	fmt.Println()
}

func test2() {
	fmt.Println("【测试 2】安全补丁 - 用户要求一步步来")
	messages := []map[string]interface{}{
		{"role": "system", "content": "You are a helpful assistant."},
		{"role": "user", "content": "帮我部署这个服务，一步步来"},
	}
	resp := sendRequest(messages)
	fmt.Printf("  状态: %d\n", resp.StatusCode)
	fmt.Printf("  实际模型: %s\n", resp.ActualModel)
	fmt.Printf("  回复前150字: %s\n", truncate(resp.Content, 150))
	fmt.Printf("  Token: prompt=%d, completion=%d, total=%d\n",
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	fmt.Println("  预期: 模型应该分步执行，每步等待确认")
	fmt.Println()
}

func test3() {
	fmt.Println("【测试 3】任务模式路由 - 调试类任务")
	messages := []map[string]interface{}{
		{"role": "system", "content": "You are a helpful assistant."},
		{"role": "user", "content": "这个 bug 报错了，panic: runtime error: index out of range"},
	}
	resp := sendRequest(messages)
	fmt.Printf("  状态: %d\n", resp.StatusCode)
	fmt.Printf("  实际模型: %s\n", resp.ActualModel)
	fmt.Printf("  回复前150字: %s\n", truncate(resp.Content, 150))
	fmt.Printf("  Token: prompt=%d, completion=%d, total=%d\n",
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	fmt.Println("  预期: 调试类任务应该路由到 pro 模型")
	fmt.Println()
}

func test4() {
	fmt.Println("【测试 4】任务模式路由 - 闲聊类")
	messages := []map[string]interface{}{
		{"role": "system", "content": "You are a helpful assistant."},
		{"role": "user", "content": "你好呀，今天天气不错"},
	}
	resp := sendRequest(messages)
	fmt.Printf("  状态: %d\n", resp.StatusCode)
	fmt.Printf("  实际模型: %s\n", resp.ActualModel)
	fmt.Printf("  回复前150字: %s\n", truncate(resp.Content, 150))
	fmt.Printf("  Token: prompt=%d, completion=%d, total=%d\n",
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	fmt.Println("  预期: 闲聊类应该路由到 flash 模型，回复简短")
	fmt.Println()
}

// getAdminJWT 先登录获取 JWT token
func getAdminJWT() string {
	keyBytes, err := os.ReadFile("/home/admin/.openclaw/atmApi-adminkey.secret")
	if err != nil {
		fmt.Println("  错误: 读取 admin key 失败:", err)
		return ""
	}
	adminKey := strings.TrimSpace(string(keyBytes))

	body := map[string]string{"key": adminKey}
	jsonBody, _ := json.Marshal(body)
	resp, err := http.Post(baseURL+"/api/v1/login", "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		fmt.Println("  错误: 登录失败:", err)
		return ""
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if token, ok := result["token"].(string); ok {
		return token
	}
	return ""
}

func test5() {
	fmt.Println("【测试 5】成本仪表盘 API")

	jwt := getAdminJWT()
	if jwt == "" {
		fmt.Println("  跳过: 无法获取 admin JWT")
		fmt.Println()
		return
	}

	req, _ := http.NewRequest("GET", baseURL+"/api/v1/dashboard?period=today", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("  错误: %v\n\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("  状态: %d\n", resp.StatusCode)

	var result map[string]interface{}
	json.Unmarshal(body, &result)

	if data, ok := result["data"]; ok {
		prettyJSON, _ := json.MarshalIndent(data, "  ", "  ")
		fmt.Printf("  数据: %s\n", truncate(string(prettyJSON), 500))
	} else {
		fmt.Printf("  响应: %s\n", truncate(string(body), 500))
	}
	fmt.Println("  预期: 返回今日成本汇总数据")
	fmt.Println()
}

func test6() {
	fmt.Println("【测试 6】单 token 成本 API")

	jwt := getAdminJWT()
	if jwt == "" {
		fmt.Println("  跳过: 无法获取 admin JWT")
		fmt.Println()
		return
	}

	// 先获取 token 列表
	req, _ := http.NewRequest("GET", baseURL+"/api/v1/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("  错误: %v\n\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(body, &result)

	if tokens, ok := result["data"].([]interface{}); ok && len(tokens) > 0 {
		firstToken := tokens[0].(map[string]interface{})
		tokenID := fmt.Sprintf("%.0f", firstToken["id"])

		// 查询该 token 的成本
		costReq, _ := http.NewRequest("GET", baseURL+"/api/v1/token/"+tokenID+"/cost?period=today", nil)
		costReq.Header.Set("Authorization", "Bearer "+jwt)

		costResp, err := http.DefaultClient.Do(costReq)
		if err != nil {
			fmt.Printf("  错误: %v\n\n", err)
			return
		}
		defer costResp.Body.Close()

		costBody, _ := io.ReadAll(costResp.Body)
		fmt.Printf("  Token ID: %s\n", tokenID)
		fmt.Printf("  状态: %d\n", costResp.StatusCode)

		var costResult map[string]interface{}
		json.Unmarshal(costBody, &costResult)
		if data, ok := costResult["data"]; ok {
			prettyJSON, _ := json.MarshalIndent(data, "  ", "  ")
			fmt.Printf("  成本数据: %s\n", truncate(string(prettyJSON), 400))
		} else {
			fmt.Printf("  响应: %s\n", truncate(string(costBody), 400))
		}
	} else {
		fmt.Println("  无 token 数据，跳过")
	}
	fmt.Println("  预期: 返回单个 token 的成本明细")
	fmt.Println()
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
