package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"atmapi/internal/model"
	"atmapi/internal/service"

	"github.com/gin-gonic/gin"
)

// ============================================================
// MCP (Model Context Protocol) 端点
// ============================================================
//
// 暴露两个工具供外部 AI Agent 调用：
//   1. query_usage  — 查询 Token 余额和配额
//   2. create_renewal — 生成续费/升级支付链接
//
// 协议：JSON-RPC 2.0 over HTTP
// 端点：
//   GET  /mcp           → SSE 流（Server-Sent Events）
//   POST /mcp           → JSON-RPC 请求/响应
//
// 认证：Bearer Token（atmApi 的 API Key）
// ============================================================

// JSON-RPC 2.0 结构
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCP 工具定义
var mcpTools = []map[string]interface{}{
	{
		"name":        "query_usage",
		"description": "查询 atmApi Token 的剩余配额和使用情况。传入 token（API Key），返回 5小时/每日/每周/每月 的已用/剩余次数、套餐信息、到期时间等。",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token": map[string]interface{}{
					"type":        "string",
					"description": "atmApi 的 API Key（以 sk- 开头）",
				},
			},
			"required": []string{"token"},
		},
	},
	{
		"name":        "create_renewal",
		"description": "生成续费或升级套餐的支付链接。传入 token 和目标套餐名，返回支付宝支付链接。",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token": map[string]interface{}{
					"type":        "string",
					"description": "atmApi 的 API Key（以 sk- 开头）",
				},
				"plan": map[string]interface{}{
					"type":        "string",
					"description": "目标套餐：basic(¥29.9), pro(¥49.9), premium(¥89)",
					"enum":         []string{"basic", "pro", "premium"},
				},
			},
			"required": []string{"token", "plan"},
		},
	},
}

// mcpHandle 统一 MCP 入口
// GET  → SSE 流（保持连接，推送工具列表）
// POST → JSON-RPC 请求/响应
func mcpHandle(c *gin.Context) {
	switch c.Request.Method {
	case http.MethodGet:
		mcpSSE(c)
	case http.MethodPost:
		mcpJSONRPC(c)
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
	}
}

// mcpSSE — SSE 传输模式
func mcpSSE(c *gin.Context) {
	// 设置 SSE 头部
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	// 发送 endpoint 事件（告诉客户端往哪 POST）
	endpointURL := fmt.Sprintf("http://%s/mcp", c.Request.Host)
	fmt.Fprintf(c.Writer, "event: endpoint\ndata: %s\n\n", endpointURL)
	flusher.Flush()

	// 发送 initialize 响应
	initResp := JSONRPCResponse{
		JSONRPC: "2.0",
		Result: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "atmapi",
				"version": "2.0.1",
			},
		},
	}
	data, _ := json.Marshal(initResp)
	fmt.Fprintf(c.Writer, "event: message\ndata: %s\n\n", string(data))
	flusher.Flush()

	// 保持连接，等待客户端请求（最长 5 分钟超时）
	timeout := time.NewTimer(5 * time.Minute)
	defer timeout.Stop()

	notify := c.Writer.CloseNotify()
	for {
		select {
		case <-notify:
			return
		case <-timeout.C:
			return
		case <-time.After(30 * time.Second):
			// 发心跳保活
			fmt.Fprintf(c.Writer, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

// mcpJSONRPC — JSON-RPC 请求处理
func mcpJSONRPC(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, JSONRPCResponse{
			JSONRPC: "2.0",
			Error:   &RPCError{Code: -32700, Message: "Parse error"},
		})
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.JSON(http.StatusBadRequest, JSONRPCResponse{
			JSONRPC: "2.0",
			Error:   &RPCError{Code: -32700, Message: "Parse error"},
		})
		return
	}

	// JSON-RPC 方法路由
	switch req.Method {
	case "initialize":
		c.JSON(http.StatusOK, JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]interface{}{
					"tools": map[string]interface{}{},
				},
				"serverInfo": map[string]interface{}{
					"name":    "atmapi",
					"version": "2.0.1",
				},
			},
		})

	case "tools/list":
		c.JSON(http.StatusOK, JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"tools": mcpTools,
			},
		})

	case "tools/call":
		mcpToolCall(c, &req)

	case "ping":
		c.JSON(http.StatusOK, JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{},
		})

	default:
		c.JSON(http.StatusOK, JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)},
		})
	}
}

// mcpToolCall — 执行工具调用
func mcpToolCall(c *gin.Context, req *JSONRPCRequest) {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		c.JSON(http.StatusOK, JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32602, Message: "Invalid params"},
		})
		return
	}

	switch params.Name {
	case "query_usage":
		mcpQueryUsage(c, req.ID, params.Arguments)
	case "create_renewal":
		mcpCreateRenewal(c, req.ID, params.Arguments)
	default:
		c.JSON(http.StatusOK, JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32602, Message: fmt.Sprintf("Unknown tool: %s", params.Name)},
		})
	}
}

// mcpQueryUsage — 查询 Token 使用情况
func mcpQueryUsage(c *gin.Context, id interface{}, args map[string]interface{}) {
	tokenKey, ok := args["token"].(string)
	if !ok || tokenKey == "" {
		mcpToolError(c, id, "缺少 token 参数")
		return
	}

	var token model.Token
	if err := model.DB.Where("key = ?", tokenKey).First(&token).Error; err != nil {
		mcpToolError(c, id, "Token 不存在")
		return
	}

	now := time.Now().Unix()
	rlResult := service.CheckRateLimit(&token)

	// 状态
	status := "active"
	if token.Status == 2 {
		status = "disabled"
	} else if token.ExpiredTime > 0 && now > token.ExpiredTime {
		status = "expired"
	} else if token.ActivatedAt == 0 {
		status = "waiting"
	}

	// 到期信息
	var expireDate string
	var remainingDays int
	if token.ExpiredTime > 0 {
		remainingDays = int((token.ExpiredTime - now) / 86400)
		if remainingDays < 0 {
			remainingDays = 0
		}
		expireDate = time.Unix(token.ExpiredTime, 0).Format("2006-01-02 15:04:05")
	} else {
		expireDate = "never"
		remainingDays = -1
	}

	// 套餐信息
	planName := token.RateLimitGroup
	planDisplay := planName
	var plan model.Plan
	if planName != "" {
		if p, err := service.GetPlan(planName); err == nil {
			planDisplay = p.DisplayName
			plan = *p
		}
	}

	result := map[string]interface{}{
		"token_name":    token.Name,
		"plan":          planName,
		"plan_display":  planDisplay,
		"status":        status,
		"quota_5h": map[string]interface{}{
			"used":      rlResult.Used5h,
			"limit":     rlResult.Limit5h,
			"remaining": rlResult.Limit5h - rlResult.Used5h,
		},
		"quota_daily": map[string]interface{}{
			"used":      rlResult.UsedDaily,
			"limit":     rlResult.LimitDaily,
			"remaining": rlResult.LimitDaily - rlResult.UsedDaily,
		},
		"quota_weekly": map[string]interface{}{
			"used":      rlResult.UsedWeekly,
			"limit":     rlResult.LimitWeekly,
			"remaining": rlResult.LimitWeekly - rlResult.UsedWeekly,
		},
		"quota_monthly": map[string]interface{}{
			"used":      rlResult.UsedMonthly,
			"limit":     rlResult.LimitMonthly,
			"remaining": rlResult.LimitMonthly - rlResult.UsedMonthly,
		},
		"max_output_tokens": plan.MaxOutputTokens,
		"max_input_tokens":  plan.MaxInputTokens,
		"expired_at":        expireDate,
		"remaining_days":    remainingDays,
	}

	resultJSON, _ := json.MarshalIndent(result, "", "  ")

	c.JSON(http.StatusOK, JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": string(resultJSON),
				},
			},
		},
	})
}

// mcpCreateRenewal — 生成续费/升级链接
func mcpCreateRenewal(c *gin.Context, id interface{}, args map[string]interface{}) {
	tokenKey, ok := args["token"].(string)
	if !ok || tokenKey == "" {
		mcpToolError(c, id, "缺少 token 参数")
		return
	}
	planName, ok := args["plan"].(string)
	if !ok || planName == "" {
		mcpToolError(c, id, "缺少 plan 参数")
		return
	}

	// 验证 Token 存在
	var token model.Token
	if err := model.DB.Where("key = ?", tokenKey).First(&token).Error; err != nil {
		mcpToolError(c, id, "Token 不存在")
		return
	}

	// 验证套餐存在
	plan, err := service.GetPlan(planName)
	if err != nil {
		mcpToolError(c, id, fmt.Sprintf("套餐 %s 不存在", planName))
		return
	}

	// 生成支付链接（复用现有订单逻辑）
	orderNo := fmt.Sprintf("ATM%s%d", time.Now().Format("20060102"), time.Now().UnixNano()/1e6)
	price, _ := parsePrice(plan.Price)

	order := &model.Order{
		OrderNo:    orderNo,
		UserOpenID: "",
		PlanName:   planName,
		Price:      price,
		Status:     "pending",
		TokenName:  token.Name, // 续费绑定旧 token
	}

	if err := model.DB.Create(order).Error; err != nil {
		mcpToolError(c, id, "创建订单失败")
		return
	}

	// 生成支付 URL
	var payURL string
	if AlipayReady() {
		payURL = buildAlipayPayURL(order.OrderNo, plan.DisplayName, price)
	} else {
		// 支付宝未配置，返回 web 支付页
		payURL = fmt.Sprintf("http://%s/pay?order=%s", c.Request.Host, orderNo)
	}

	result := map[string]interface{}{
		"order_id":    orderNo,
		"plan":        planName,
		"plan_display": plan.DisplayName,
		"price":       plan.Price,
		"pay_url":     payURL,
		"message":     fmt.Sprintf("续费/升级订单已创建，套餐：%s，价格：¥%s", plan.DisplayName, plan.Price),
	}

	resultJSON, _ := json.MarshalIndent(result, "", "  ")

	c.JSON(http.StatusOK, JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": string(resultJSON),
				},
			},
		},
	})
}

// mcpToolError — 返回工具调用错误
func mcpToolError(c *gin.Context, id interface{}, message string) {
	log.Printf("[MCP] 工具调用错误: %s", message)
	c.JSON(http.StatusOK, JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": fmt.Sprintf("❌ %s", message),
				},
			},
			"isError": true,
		},
	})
}

// parsePrice 解析价格字符串
func parsePrice(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

// buildAlipayPayURL 构建支付宝支付链接
// 如果 AlipayReady()，使用 alipay.go 中的 CreateAlipayOrder 生成真实支付链接
func buildAlipayPayURL(orderNo, subject string, amount float64) string {
	payURL, err := CreateAlipayOrder(orderNo, fmt.Sprintf("%.2f", amount), subject)
	if err != nil {
		log.Printf("[MCP] 支付链接生成失败: %v", err)
		return ""
	}
	return payURL
}

// extractTokenFromAuth 从 Authorization 头提取 Bearer token
func extractTokenFromAuth(authHeader string) string {
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}
