package service

import (
	"encoding/json"
	"strings"
)

// SSETermination 流式响应终态分类
// V1.5: 柯大侠要求统一终态判断
type SSETermination struct {
	SawDone      bool // 出现 data: [DONE]
	SawContent   bool // 至少一个 chunk 有非空 content
	SawToolCalls bool // 至少一个 chunk 有 tool_calls
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
		return
	}

	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			s.SawContent = true
		}
		if choice.Delta.Refusal != "" {
			s.SawRefusal = true
		}
		if choice.Delta.ToolCalls != nil {
			s.SawToolCalls = true
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
	return NonStreamTermination{
		HasContent:   choice.Message.Content != "",
		HasToolCalls: choice.Message.ToolCalls != nil,
		HasRefusal:   choice.Message.Refusal != "",
		IsEmpty:      choice.Message.Content == "" && choice.Message.ToolCalls == nil && choice.Message.Refusal == "",
	}
}

// IsValidPreferenceModel 判断模型名是否可写入偏好缓存
// V1.5: 禁止写入元模型、空字符串、临时视觉模型
func IsValidPreferenceModel(model string) bool {
	if model == "" {
		return false
	}
	lower := strings.ToLower(model)
	if lower == "deepseek-a4" {
		return false // 元模型，不能写入
	}
	if lower == "qwen3.7-plus" || lower == "qwen3.6-plus" || lower == "qwen3.5-plus" {
		return false // 临时视觉模型
	}
	return true
}
