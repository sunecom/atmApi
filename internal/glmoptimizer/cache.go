package glmoptimizer

import (
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type CacheDecision struct {
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason"`
}

var cacheRequestFields = map[string]bool{
	"model": true, "messages": true, "temperature": true, "max_tokens": true,
	"reasoning": true, "response_format": true, "top_p": true, "stop": true,
	"seed": true, "n": true, "session_id": true, "provider": true, "stream": true,
}

func EvaluateCacheEligibility(body []byte) CacheDecision {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return CacheDecision{Reason: "invalid_json"}
	}
	for field := range request {
		if !cacheRequestFields[field] {
			return CacheDecision{Reason: "unsupported_field:" + field}
		}
	}
	var temperature json.Number
	raw, found := request["temperature"]
	if !found || json.Unmarshal(raw, &temperature) != nil || temperature.String() == "" {
		return CacheDecision{Reason: "temperature_must_be_explicit_zero"}
	}
	value, err := temperature.Float64()
	if err != nil || value != 0 {
		return CacheDecision{Reason: "temperature_must_be_explicit_zero"}
	}
	if raw, found := request["stream"]; found {
		var stream bool
		if json.Unmarshal(raw, &stream) != nil || stream {
			return CacheDecision{Reason: "streaming_request"}
		}
	}
	if raw, found := request["n"]; found {
		var n int
		if json.Unmarshal(raw, &n) != nil || n != 1 {
			return CacheDecision{Reason: "n_must_equal_one"}
		}
	}
	var messages []struct {
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		ToolCalls json.RawMessage `json:"tool_calls"`
	}
	if json.Unmarshal(request["messages"], &messages) != nil {
		return CacheDecision{Reason: "invalid_messages"}
	}
	for _, message := range messages {
		if message.Role == "tool" || (len(message.ToolCalls) > 0 && string(message.ToolCalls) != "null" && string(message.ToolCalls) != "[]") {
			return CacheDecision{Reason: "tool_context"}
		}
		if len(message.Content) == 0 || string(message.Content) == "null" {
			continue
		}
		var text string
		if json.Unmarshal(message.Content, &text) != nil {
			return CacheDecision{Reason: "multimodal_content"}
		}
	}
	return CacheDecision{Eligible: true, Reason: "deterministic_text_request"}
}

type CacheConfig struct {
	TTL           time.Duration
	MaxEntries    int
	MaxEntryBytes int
	MaxTotalBytes int
}

type responseCacheEntry struct {
	key       string
	response  []byte
	expiresAt time.Time
}

type GLM52ResponseCache struct {
	mu         sync.Mutex
	config     CacheConfig
	entries    map[string]*list.Element
	lru        *list.List
	totalBytes int
}

func NewResponseCache(config CacheConfig) *GLM52ResponseCache {
	if config.TTL <= 0 {
		config.TTL = 5 * time.Minute
	}
	if config.MaxEntries <= 0 {
		config.MaxEntries = 1000
	}
	if config.MaxEntryBytes <= 0 {
		config.MaxEntryBytes = 2 << 20
	}
	if config.MaxTotalBytes <= 0 {
		config.MaxTotalBytes = 64 << 20
	}
	return &GLM52ResponseCache{config: config, entries: make(map[string]*list.Element), lru: list.New()}
}

func (c *GLM52ResponseCache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, found := c.entries[key]
	if !found {
		return nil, false
	}
	entry := element.Value.(*responseCacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.remove(element)
		return nil, false
	}
	c.lru.MoveToFront(element)
	return append([]byte(nil), entry.response...), true
}

// Set admits only complete, valid, content-bearing JSON responses.
func (c *GLM52ResponseCache) Set(key string, response []byte) bool {
	if len(response) == 0 || len(response) > c.config.MaxEntryBytes || len(response) > c.config.MaxTotalBytes ||
		!json.Valid(response) || ClassifyNonStream(response).State != TerminalSuccessContent || responseHasToolCalls(response) {
		return false
	}
	copyOfResponse := append([]byte(nil), response...)
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, found := c.entries[key]; found {
		entry := element.Value.(*responseCacheEntry)
		c.totalBytes -= len(entry.response)
		entry.response, entry.expiresAt = copyOfResponse, time.Now().Add(c.config.TTL)
		c.totalBytes += len(copyOfResponse)
		c.lru.MoveToFront(element)
		for c.totalBytes > c.config.MaxTotalBytes {
			c.remove(c.lru.Back())
		}
		return true
	}
	element := c.lru.PushFront(&responseCacheEntry{key: key, response: copyOfResponse, expiresAt: time.Now().Add(c.config.TTL)})
	c.entries[key] = element
	c.totalBytes += len(copyOfResponse)
	for len(c.entries) > c.config.MaxEntries || c.totalBytes > c.config.MaxTotalBytes {
		c.remove(c.lru.Back())
	}
	return true
}

func responseHasToolCalls(response []byte) bool {
	var envelope struct {
		Choices []struct {
			Message struct {
				ToolCalls json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(response, &envelope) != nil {
		return true
	}
	for _, choice := range envelope.Choices {
		var calls []json.RawMessage
		if len(choice.Message.ToolCalls) > 0 && string(choice.Message.ToolCalls) != "null" {
			if json.Unmarshal(choice.Message.ToolCalls, &calls) != nil || len(calls) > 0 {
				return true
			}
		}
	}
	return false
}

func (c *GLM52ResponseCache) remove(element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(*responseCacheEntry)
	delete(c.entries, entry.key)
	c.totalBytes -= len(entry.response)
	c.lru.Remove(element)
}

type flightCall struct {
	done  chan struct{}
	value []byte
	err   error
}

type FlightGroup struct {
	mu    sync.Mutex
	calls map[string]*flightCall
}

func (g *FlightGroup) Do(ctx context.Context, key string, fn func() ([]byte, error)) ([]byte, bool, error) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*flightCall)
	}
	if call, found := g.calls[key]; found {
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, true, ctx.Err()
		case <-call.done:
			return append([]byte(nil), call.value...), true, call.err
		}
	}
	call := &flightCall{done: make(chan struct{})}
	g.calls[key] = call
	g.mu.Unlock()
	call.value, call.err = fn()
	call.value = append([]byte(nil), call.value...)
	g.mu.Lock()
	delete(g.calls, key)
	close(call.done)
	g.mu.Unlock()
	return append([]byte(nil), call.value...), false, call.err
}

func (d CacheDecision) String() string {
	return fmt.Sprintf("eligible=%t reason=%s", d.Eligible, d.Reason)
}
