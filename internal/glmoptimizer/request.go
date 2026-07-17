package glmoptimizer

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const ModelGLM52 = "glm-5.2"

var (
	ErrInvalidRequest = errors.New("invalid GLM-5.2 request")
	ErrMissingModel   = fmt.Errorf("%w: model is required", ErrInvalidRequest)
	ErrMissingMessage = fmt.Errorf("%w: messages are required", ErrInvalidRequest)
)

// Request contains only the fields the GLM-5.2 entry gate needs. Unknown
// OpenAI-compatible fields remain in the raw request body and are preserved.
type Request struct {
	Model    string            `json:"model"`
	Messages []json.RawMessage `json:"messages"`
	Stream   bool              `json:"stream,omitempty"`
}

// HasImage 检测请求是否包含图片内容（image_url 或 base64 image）
func (r Request) HasImage() bool {
	for _, msg := range r.Messages {
		var m struct {
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(msg, &m); err != nil || m.Content == nil {
			continue
		}
		// content 可能是字符串或数组
		var arr []json.RawMessage
		if err := json.Unmarshal(m.Content, &arr); err != nil {
			continue
		}
		for _, part := range arr {
			var p struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(part, &p); err != nil {
				continue
			}
			if p.Type == "image_url" {
				return true
			}
		}
	}
	return false
}

// ParseRequest validates the minimum OpenAI chat-completions envelope without
// rejecting provider extensions that later pipeline stages need to preserve.
func ParseRequest(body []byte) (Request, error) {
	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		return Request{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if strings.TrimSpace(req.Model) == "" {
		return Request{}, ErrMissingModel
	}
	if req.Messages == nil {
		return Request{}, ErrMissingMessage
	}
	return req, nil
}

// IsGLM52Request returns true when either the public model or any attached
// package label belongs to the GLM-5.2 product line.
func IsGLM52Request(requestedModel string, packageLabels ...string) bool {
	if normalizeLabel(requestedModel) == ModelGLM52 {
		return true
	}
	for _, label := range packageLabels {
		normalized := normalizeLabel(label)
		if normalized == ModelGLM52 || normalized == "glm52" ||
			strings.HasPrefix(normalized, "glm-5.2-") ||
			strings.HasPrefix(normalized, "glm52-") {
			return true
		}
	}
	return false
}

func normalizeLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.ReplaceAll(value, "_", "-")
}

// LockModel rewrites only the top-level model field and preserves every other
// request field. Provider-specific mapping happens later inside the selected
// GLM-5.2 channel.
func LockModel(body []byte) ([]byte, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	locked, err := json.Marshal(ModelGLM52)
	if err != nil {
		return nil, fmt.Errorf("encode locked model: %w", err)
	}
	request["model"] = locked
	result, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode locked request: %w", err)
	}
	return result, nil
}
