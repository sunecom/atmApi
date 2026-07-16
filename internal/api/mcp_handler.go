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
// MCP (Model Context Protocol) 端点 — AiToMoney 团队出品
// ============================================================
//
// 暴露三个工具供外部 AI Agent 调用：
//   1. query_usage    — 查询 Token 余额和配额
//   2. create_renewal — 生成续费/升级支付链接
//   3. list_models    — 查看可用模型列表和介绍
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
		"description": "查询 atmApi Token 的剩余配额和使用情况（deepseek-a4 次数制套餐）。返回 5小时/每日/每周/每月 的已用/剩余次数、套餐信息、到期时间等。token 参数可选——如果不传，自动使用当前调用者的 API Key。",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token": map[string]interface{}{
					"type":        "string",
					"description": "atmApi 的 API Key（可选，不传则自动使用当前调用者的 token）",
				},
			},
			"required": []string{},
		},
	},
	{
		"name":        "query_glm_usage",
		"description": "查询 GLM-5.2 Token 的点数余额和使用情况（GLM-5.2 点数制套餐）。返回月度总点数、已用、剩余、5小时/每日/每周限额、输入输出上限等。token 参数可选——如果不传，自动使用当前调用者的 API Key。",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token": map[string]interface{}{
					"type":        "string",
					"description": "atmApi 的 API Key（可选，不传则自动使用当前调用者的 token）",
				},
			},
			"required": []string{},
		},
	},
	{
		"name":        "create_renewal",
		"description": "生成续费或升级套餐的指引。传入目标套餐名，返回带 token 的 token-info 页面链接，用户可在页面上查看套餐详情并完成支付。支持 deepseek-a4 和 GLM-5.2 套餐。token 参数可选——如果不传，自动使用当前调用者的 API Key。可用套餐名请通过 list_models 查询。",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token": map[string]interface{}{
					"type":        "string",
					"description": "atmApi 的 API Key（可选，不传则自动使用当前调用者的 token）",
				},
				"plan": map[string]interface{}{
					"type":        "string",
					"description": "目标套餐名（如 basic, pro, flagship, glm-basic, glm-standard, glm-pro 等）。先调 list_models 获取所有可用套餐列表。",
				},
			},
			"required": []string{"plan"},
		},
	},
	{
		"name":        "list_models",
		"description": "查看 atmApi 所有可用模型和套餐。包含 deepseek-a4（智能路由）和 GLM-5.2（企业级多平台路由）的核心能力、工作原理、适用场景和套餐价格。AiToMoney 团队出品。",
		"inputSchema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
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
				"name":     "atmApi",
				"version":  "2.0.2", "vendor": "AiToMoney 团队出品 🚀", "homepage": "https://atmapi.aitomoney.online",
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
					"name":     "atmApi",
					"version":  "2.0.2", "vendor": "AiToMoney 团队出品 🚀", "homepage": "https://atmapi.aitomoney.online",
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

	// 🚀 自动注入调用者 token：从 Authorization Bearer 头提取
	// 智能体调用 query_usage / create_renewal 时不需要手动传 token
	if params.Arguments == nil {
		params.Arguments = map[string]interface{}{}
	}
	if _, hasToken := params.Arguments["token"]; !hasToken {
		authHeader := c.GetHeader("Authorization")
		callerToken := extractTokenFromAuth(authHeader)
		if callerToken != "" {
			params.Arguments["token"] = callerToken
			log.Printf("[MCP] 自动注入调用者 token (tool=%s, auth_len=%d)", params.Name, len(authHeader))
		} else {
			log.Printf("[MCP] ⚠️ 未找到 Authorization 头 (tool=%s, auth_header=%q, all_headers=%v)", params.Name, authHeader, c.Request.Header)
		}
	}

	switch params.Name {
	case "query_usage":
		mcpQueryUsage(c, req.ID, params.Arguments)
	case "query_glm_usage":
		mcpQueryGLMUsage(c, req.ID, params.Arguments)
	case "create_renewal":
		mcpCreateRenewal(c, req.ID, params.Arguments)
	case "list_models":
		mcpListModels(c, req.ID)
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

	token, err := model.FindByKey(tokenKey)
	if err != nil {
		mcpToolError(c, id, "Token 不存在")
		return
	}

	now := time.Now().Unix()
	rlResult := service.CheckRateLimit(token)

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

	// 快到期时加上续费引导提示
	if remainingDays >= 0 && remainingDays <= 7 {
		result["renewal_hint"] = fmt.Sprintf("套餐还有 %d 天到期，如需续费请输入\"我要续费\"", remainingDays)
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

// mcpQueryGLMUsage — 查询 GLM-5.2 Token 点数使用情况
func mcpQueryGLMUsage(c *gin.Context, id interface{}, args map[string]interface{}) {
	tokenKey, ok := args["token"].(string)
	if !ok || tokenKey == "" {
		mcpToolError(c, id, "缺少 token 参数")
		return
	}

	token, err := model.FindByKey(tokenKey)
	if err != nil {
		mcpToolError(c, id, "Token 不存在")
		return
	}

	// 查询 GLM 点数账本
	now := time.Now()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	var ledger model.GLMPointsLedger
	err = model.DB.Where("token_id = ? AND period_start = ?", token.ID, periodStart).First(&ledger).Error
	if err != nil {
		// 账本不存在，说明不是 GLM 套餐用户
		mcpToolError(c, id, "该 Token 未开通 GLM-5.2 套餐或账本未初始化")
		return
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

	// 状态判断
	status := "active"
	if token.Status == 2 {
		status = "disabled"
	} else if token.ExpiredTime > 0 && now.Unix() > token.ExpiredTime {
		status = "expired"
	} else if token.ActivatedAt == 0 {
		status = "waiting"
	}

	// 到期信息
	var expireDate string
	var remainingDays int
	if token.ExpiredTime > 0 {
		remainingDays = int((token.ExpiredTime - now.Unix()) / 86400)
		if remainingDays < 0 {
			remainingDays = 0
		}
		expireDate = time.Unix(token.ExpiredTime, 0).Format("2006-01-02 15:04:05")
	} else {
		expireDate = "never"
		remainingDays = -1
	}

	// 计算剩余点数
	remainingPoints := ledger.TotalPoints - ledger.UsedPoints
	fiveHourRemaining := ledger.FiveHourPoints - ledger.FiveHourUsed
	dailyRemaining := ledger.DailyPoints - ledger.DailyUsed

	result := map[string]interface{}{
		"token_name":   token.Name,
		"plan":         planName,
		"plan_display": planDisplay,
		"status":       status,
		"points": map[string]interface{}{
			"total":     ledger.TotalPoints,
			"used":      ledger.UsedPoints,
			"remaining": remainingPoints,
			"usage_pct": fmt.Sprintf("%.1f%%", float64(ledger.UsedPoints)/float64(ledger.TotalPoints)*100),
		},
		"quota_5h": map[string]interface{}{
			"limit":     ledger.FiveHourPoints,
			"used":      ledger.FiveHourUsed,
			"remaining": fiveHourRemaining,
		},
		"quota_daily": map[string]interface{}{
			"limit":     ledger.DailyPoints,
			"used":      ledger.DailyUsed,
			"remaining": dailyRemaining,
		},
		"period": map[string]interface{}{
			"start": ledger.PeriodStart.Format("2006-01-02 15:04:05"),
			"end":   ledger.PeriodEnd.Format("2006-01-02 15:04:05"),
		},
		"max_input_tokens":  plan.MaxInputTokens,
		"max_output_tokens": plan.MaxOutputTokens,
		"expired_at":        expireDate,
		"remaining_days":    remainingDays,
		"pricing": map[string]interface{}{
			"standard_price_per_point": ledger.StandardPricePerPoint,
			"description":              "100 点 = ¥1，1 点 = ¥0.01",
			"formula":                  "每次扣点 = ⌈输入tokens × 0.008 + 输出tokens × 0.028⌉",
		},
	}

	// 快到期时加上续费引导提示
	if remainingDays >= 0 && remainingDays <= 7 {
		result["renewal_hint"] = fmt.Sprintf("套餐还有 %d 天到期，如需续费请输入\"我要续费\"", remainingDays)
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
	token, err := model.FindByKey(tokenKey)
	if err != nil {
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

	// 不再直接生成支付链接，改为引导用户去 token-info 页面自行操作
	// 链接和 token 分开返回，用户手动复制 token 去页面查询

	result := map[string]interface{}{
		"plan":         planName,
		"plan_display": plan.DisplayName,
		"price":        plan.Price,
		"token":        tokenKey,
		"manage_url":   "https://pay.aitomoney.online/token-info",
		"message":      fmt.Sprintf("请打开链接 https://pay.aitomoney.online/token-info ，输入 Token：%s 查看详情并续费/升级", tokenKey),
	}

	// 用紧凑格式，避免长 URL 被换行截断
	resultJSON, _ := json.Marshal(result)

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

// mcpListModels — 当前模型介绍（deepseek-a4），套餐信息从数据库动态读取
func mcpListModels(c *gin.Context, id interface{}) {
	// 动态读取套餐列表
	var plans []model.Plan
	model.DB.Find(&plans)
	
	// 按模型分组套餐
	var deepseekPlans []map[string]interface{}
	var glmPlans []map[string]interface{}
	
	for _, p := range plans {
		planInfo := map[string]interface{}{
			"name":          p.Name,
			"display":       p.DisplayName,
			"price":         p.Price,
			"quota_5h":      p.Hourly5Max,
			"quota_monthly": p.MonthlyMax,
		}
		
		// 根据套餐名称判断属于哪个模型
		if strings.HasPrefix(p.Name, "glm") {
			// GLM-5.2 套餐，添加输入输出上限
			planInfo["max_input_tokens"] = p.MaxInputTokens
			planInfo["max_output_tokens"] = p.MaxOutputTokens
			glmPlans = append(glmPlans, planInfo)
		} else {
			// deepseek-a4 套餐
			deepseekPlans = append(deepseekPlans, planInfo)
		}
	}

	result := map[string]interface{}{
		"vendor": "AiToMoney 团队出品 🚀",
		"models": []map[string]interface{}{
			{
				"model":          "deepseek-a4",
				"display_name":   "DeepSeek A4（智能路由）",
				"capabilities":   "文本 + 图片理解",
				"context_window": "1,000,000 tokens（1M）",
				"max_output":     "384,000 tokens",
				"description":    "atmApi 智能路由旗舰模型。根据请求内容自动选择最优后端：图片→Qwen3.7-Plus（多模态），简单文本→DeepSeek-V4-Flash（快速便宜），复杂推理→DeepSeek-V4-Pro（深度思考）。一模型打通所有场景。",
				"routing":        "图片→qwen3.7-plus | 简单文本→deepseek-v4-flash | 复杂文本→deepseek-v4-pro | tool_calls→锁模型",
				"billing":        "次数制（每次请求扣1次配额）",
				"plans":          deepseekPlans,
			},
			{
				"model":          "glm-5.2",
				"display_name":   "GLM-5.2（企业级多平台路由）",
				"capabilities":   "文本推理",
				"context_window": "128,000 tokens（128K）",
				"max_output":     "32,768 tokens",
				"description":    "企业级 GLM-5.2 多平台智能路由，点数灵活计费。多节点负载均衡，自动故障切换，确保高可用。适合需要深度推理和长上下文的场景。",
				"routing":        "多平台智能路由，自动选择最优线路",
				"billing":        "点数制（按 token 用量扣点，缓存命中扣0点）",
				"pricing_formula": "每次扣点 = ⌈输入tokens × 0.008 + 输出tokens × 0.028⌉",
				"plans":          glmPlans,
			},
		},
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
