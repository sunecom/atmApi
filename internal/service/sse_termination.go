package service

import (
	"encoding/json"
	"strings"
)

// SSETermination 流式响应终态分类
// V1.5/V1.6: 柯大侠要求统一终态判断
type SSETermination struct {
	SawDone      bool // 出现 data: [DONE]
	SawContent   bool // 至少一个 chunk 有非空 content
	SawToolCalls bool // 至少一个 chunk 有非空 tool_calls
	SawRefusal   bool // 至少一个 chunk 有 refusal
	ReadError    bool // 读取中断（io.ErrUnexpectedEOF 等）
}

// IsLegalSuccess 判断是否为合法成功终态
// 只有以下条件全满足才为 true：
// 1. 有 [DONE]
// 2. 有 content 或 tool_calls 或 refusal
// 3. 没有断流
func (s SSETermination) IsLegalSuccess() bool {
	return s.SawDone &&
		(s.SawContent || s.SawToolCalls || s.SawRefusal) &&
		!s.ReadError
}

// ParseSSEChunk 解析单个 SSE data 行，更新终态
// V1.6: 空 tool_calls 数组和损坏 JSON 不算合法终态
func (s *SSETermination) ParseSSEChunk(data string) {
	if strings.TrimSpace(data) == "[DONE]" {
		s.SawDone = true
		return
	}

	var chunk struct {
		Choices []struct {
			Delta struct {
				Content   string      `json:"content"`
				Refusal   string      `json:"refusal"`
				ToolCalls interface{} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}

	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return // 损坏的 JSON 不影响终态
	}

	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			s.SawContent = true
		}
		if choice.Delta.Refusal != "" {
			s.SawRefusal = true
		}
		// V1.6: 空 tool_calls 数组不算
		if choice.Delta.ToolCalls != nil {
			if tcArr, ok := choice.Delta.ToolCalls.([]interface{}); ok && len(tcArr) > 0 {
				s.SawToolCalls = true
			}
		}
	}
}

// NonStreamTermination 非流式响应终态分类
type NonStreamTermination struct {
	HasContent   bool
	HasToolCalls bool
	HasRefusal   bool
	IsEmpty      bool
}

// IsLegalSuccess 非流式合法成功
func (n NonStreamTermination) IsLegalSuccess() bool {
	return n.HasContent || n.HasToolCalls || n.HasRefusal
}

// ParseNonStreamResponse 解析非流式响应体
// V1.6: 空 tool_calls 不算合法
func ParseNonStreamResponse(body []byte) NonStreamTermination {
	var resp struct {
		Choices []struct {
			Message struct {
				Content   string      `json:"content"`
				Refusal   string      `json:"refusal"`
				ToolCalls interface{} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return NonStreamTermination{IsEmpty: true}
	}

	if len(resp.Choices) == 0 {
		return NonStreamTermination{IsEmpty: true}
	}

	choice := resp.Choices[0]
	hasTC := false
	if choice.Message.ToolCalls != nil {
		if tcArr, ok := choice.Message.ToolCalls.([]interface{}); ok && len(tcArr) > 0 {
			hasTC = true
		}
	}
	return NonStreamTermination{
		HasContent:   choice.Message.Content != "",
		HasToolCalls: hasTC,
		HasRefusal:   choice.Message.Refusal != "",
		IsEmpty:      choice.Message.Content == "" && !hasTC && choice.Message.Refusal == "",
	}
}

// IsValidPreferenceModel 判断模型名是否可写入偏好缓存
// V1.6: 改为白名单制，只允许 flash/pro
func IsValidPreferenceModel(model string) bool {
	if model == "" {
		return false
	}
	lower := strings.ToLower(model)
	// 白名单：只有 flash/pro 能写入偏好
	return lower == "deepseek-v4-flash" || lower == "deepseek-v4-pro"
}
