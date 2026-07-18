package api

import (
	"testing"
)

func TestStripMetadata(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "标准 OpenClaw envelope",
			input: `Conversation info
chat_id: 123
sender_id: 456

Sender (untrusted metadata):
{"role": "user", "content": "hello"}

这是用户消息`,
			expected: "这是用户消息",
		},
		{
			name:     "JSON 代码块 envelope",
			input:    "```json\n{\"chat_id\": \"123\"}\n```\n\nSender (untrusted metadata):\n{\"role\": \"user\"}\n\n用户内容",
			expected: "用户内容",
		},
		{
			name:     "多个 JSON 块",
			input:    "```json\n{\"conversation info\": \"test\"}\n```\n\n```json\n{\"sender\": \"metadata\"}\n```\n\n用户消息",
			expected: "用户消息",
		},
		{
			name:     "无 envelope",
			input:    "普通用户消息",
			expected: "普通用户消息",
		},
		{
			name:     "只有空行",
			input:    "\n\n\n",
			expected: "\n\n\n",
		},
		{
			name: "media attached 标签",
			input: `[media attached: image.png]

用户消息`,
			expected: "用户消息",
		},
		{
			name: "保留用户正文中的 JSON",
			input: `Sender (untrusted metadata):
{"chat_id": "123"}

用户消息包含 {"key": "value"}`,
			expected: `用户消息包含 {"key": "value"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripMetadata(tt.input)
			if result != tt.expected {
				t.Errorf("stripMetadata() = %q, want %q", result, tt.expected)
			}
		})
	}
}
