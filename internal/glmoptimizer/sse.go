package glmoptimizer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	DefaultMaxSSEEventBytes = 1 << 20

	TerminalCodeStreamInterrupted = "GLM52_STREAM_INTERRUPTED"
)

var (
	ErrSSEEventTooLarge  = errors.New("SSE event exceeds configured limit")
	ErrStreamInterrupted = errors.New("SSE stream ended before [DONE]")
)

// FlushWriter is the minimum interface needed for latency-preserving SSE
// relaying. gin.ResponseWriter and http.Flusher-backed adapters implement it.
type FlushWriter interface {
	io.Writer
	Flush()
}

type RelayOptions struct {
	MaxEventBytes    int
	RequestStartedAt time.Time
}

type StreamUsage struct {
	Provider         string
	ActualModel      string
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	CachedTokens     int64
	CacheWriteTokens int64
	ReasoningTokens  int64
	ReportedCost     *float64
}

// RelayResult contains only bounded protocol metadata. It never contains
// content, reasoning text, or tool arguments.
type RelayResult struct {
	TTFTMs         int64
	FirstDataSeen  bool
	BytesForwarded int64
	ContentSeen    bool
	RefusalSeen    bool
	ReasoningSeen  bool
	FinishReason   string
	UsageSeen      bool
	DoneSeen       bool
	ParseErrors    int
	Outcome        TerminalOutcome
	Usage          StreamUsage
}

// RelaySSE forwards each complete upstream SSE event immediately and observes
// a side copy for terminal validation. It retains at most one event plus a
// bounded amount of tool-call protocol state; it never buffers the full stream.
func RelaySSE(ctx context.Context, dst FlushWriter, src io.Reader, options RelayOptions) (RelayResult, error) {
	if err := ctx.Err(); err != nil {
		return RelayResult{}, err
	}
	maxEventBytes := options.MaxEventBytes
	if maxEventBytes <= 0 {
		maxEventBytes = DefaultMaxSSEEventBytes
	}

	stopCancellationWatch := watchCancellation(ctx, src)
	defer stopCancellationWatch()

	reader := newSSEEventReader(src, maxEventBytes)
	observer := newStreamObserver(maxEventBytes)
	if !options.RequestStartedAt.IsZero() {
		observer.requestStartedAt = options.RequestStartedAt
	}
	var result RelayResult

	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		event, err := reader.Next()
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return result, ctxErr
			}
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				result = observer.result(result.BytesForwarded, false)
				written, writeErr := writeProtocolError(dst, TerminalCodeStreamInterrupted, "upstream stream ended before [DONE]")
				result.BytesForwarded += int64(written)
				if writeErr != nil {
					return result, writeErr
				}
				return result, ErrStreamInterrupted
			}
			return result, err
		}

		payload, hasData := eventData(event)
		if hasData && bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
			outcome := observer.finalize()
			if !outcome.Consumable {
				if err := writeTerminalError(dst, outcome); err != nil {
					return observer.result(result.BytesForwarded, true), err
				}
				result.BytesForwarded += int64(errorEventSize(outcome))
			}
			if err := writeAndFlush(dst, event); err != nil {
				return observer.result(result.BytesForwarded, true), err
			}
			result.BytesForwarded += int64(len(event))
			return observer.result(result.BytesForwarded, true), nil
		}

		if hasData {
			observer.observe(payload)
		}
		if err := writeAndFlush(dst, event); err != nil {
			return observer.result(result.BytesForwarded, false), err
		}
		result.BytesForwarded += int64(len(event))
	}
}

func watchCancellation(ctx context.Context, src io.Reader) func() {
	closer, ok := src.(io.Closer)
	if !ok || ctx.Done() == nil {
		return func() {}
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			_ = closer.Close()
		case <-done:
		}
	}()
	return func() {
		once.Do(func() { close(done) })
		<-stopped
	}
}

type sseEventReader struct {
	reader   *bufio.Reader
	maxBytes int
}

func newSSEEventReader(src io.Reader, maxBytes int) *sseEventReader {
	return &sseEventReader{reader: bufio.NewReaderSize(src, 32*1024), maxBytes: maxBytes}
}

func (r *sseEventReader) Next() ([]byte, error) {
	event := make([]byte, 0, minInt(r.maxBytes, 32*1024))
	for {
		fragment, err := r.reader.ReadSlice('\n')
		if len(fragment) > 0 {
			if len(event)+len(fragment) > r.maxBytes {
				return nil, ErrSSEEventTooLarge
			}
			event = append(event, fragment...)
			if isBlankSSELine(fragment) {
				return event, nil
			}
		}
		if err != nil {
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			if errors.Is(err, io.EOF) {
				if len(event) == 0 {
					return nil, io.EOF
				}
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
	}
}

func isBlankSSELine(line []byte) bool {
	return bytes.Equal(line, []byte("\n")) || bytes.Equal(line, []byte("\r\n"))
}

func eventData(event []byte) ([]byte, bool) {
	lines := bytes.Split(event, []byte("\n"))
	dataLines := make([][]byte, 0, 1)
	for _, line := range lines {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		value := line[len("data:"):]
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		dataLines = append(dataLines, value)
	}
	if len(dataLines) == 0 {
		return nil, false
	}
	return bytes.Join(dataLines, []byte("\n")), true
}

type streamObserver struct {
	requestStartedAt time.Time
	ttftMs           int64
	firstDataSeen    bool
	provider         string
	actualModel      string
	contentSeen      bool
	refusalSeen      bool
	reasoningSeen    bool
	finishReason     string
	usage            StreamUsage
	usageSeen        bool
	parseErrors      int
	malformed        string
	toolBytes        int
	maxToolBytes     int
	tools            map[streamToolKey]*streamTool
}

type streamToolKey struct {
	choice int
	tool   int
}

type streamTool struct {
	id        string
	name      string
	arguments strings.Builder
}

type streamEnvelope struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Choices  []struct {
		Index int `json:"index"`
		Delta struct {
			Content          json.RawMessage `json:"content"`
			Refusal          json.RawMessage `json:"refusal"`
			Reasoning        json.RawMessage `json:"reasoning"`
			ReasoningDetails json.RawMessage `json:"reasoning_details"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage json.RawMessage `json:"usage"`
}

type streamUsageEnvelope struct {
	PromptTokens     int64    `json:"prompt_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	TotalTokens      int64    `json:"total_tokens"`
	Cost             *float64 `json:"cost"`
	PromptDetails    struct {
		CachedTokens     int64 `json:"cached_tokens"`
		CacheWriteTokens int64 `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func newStreamObserver(maxToolBytes int) *streamObserver {
	return &streamObserver{requestStartedAt: time.Now(), maxToolBytes: maxToolBytes, tools: make(map[streamToolKey]*streamTool)}
}

func (o *streamObserver) observe(payload []byte) {
	if !o.firstDataSeen {
		o.firstDataSeen = true
		o.ttftMs = time.Since(o.requestStartedAt).Milliseconds()
	}
	var envelope streamEnvelope
	if err := json.Unmarshal(bytes.TrimSpace(payload), &envelope); err != nil {
		o.parseErrors++
		if o.malformed == "" {
			o.malformed = fmt.Sprintf("invalid SSE data JSON: %T", err)
		}
		return
	}
	if strings.TrimSpace(envelope.Provider) != "" {
		o.provider = envelope.Provider
	}
	if strings.TrimSpace(envelope.Model) != "" {
		o.actualModel = envelope.Model
	}
	for _, choice := range envelope.Choices {
		o.contentSeen = o.observeText(choice.Delta.Content, "delta.content") || o.contentSeen
		o.refusalSeen = o.observeText(choice.Delta.Refusal, "delta.refusal") || o.refusalSeen
		reasoningTextSeen := o.observeText(choice.Delta.Reasoning, "delta.reasoning")
		if reasoningTextSeen || rawHasValue(choice.Delta.ReasoningDetails) {
			o.reasoningSeen = true
		}
		if choice.FinishReason != nil {
			o.finishReason = *choice.FinishReason
		}
		for _, delta := range choice.Delta.ToolCalls {
			key := streamToolKey{choice: choice.Index, tool: delta.Index}
			tool := o.tools[key]
			if tool == nil {
				if !o.reserveToolState(64) {
					continue
				}
				tool = &streamTool{}
				o.tools[key] = tool
			}
			if o.reserveToolState(len(delta.ID) + len(delta.Function.Name)) {
				tool.id += delta.ID
				tool.name += delta.Function.Name
			}
			if delta.Function.Arguments != "" {
				if o.reserveToolState(len(delta.Function.Arguments)) {
					tool.arguments.WriteString(delta.Function.Arguments)
				}
			}
		}
	}
	if len(bytes.TrimSpace(envelope.Usage)) > 0 && !bytes.Equal(bytes.TrimSpace(envelope.Usage), []byte("null")) {
		var usage streamUsageEnvelope
		if err := json.Unmarshal(envelope.Usage, &usage); err != nil {
			o.parseErrors++
			if o.malformed == "" {
				o.malformed = "usage must be an object"
			}
			return
		}
		o.usageSeen = true
		o.usage = StreamUsage{
			Provider:         o.provider,
			ActualModel:      o.actualModel,
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TotalTokens:      usage.TotalTokens,
			CachedTokens:     usage.PromptDetails.CachedTokens,
			CacheWriteTokens: usage.PromptDetails.CacheWriteTokens,
			ReasoningTokens:  usage.CompletionDetails.ReasoningTokens,
			ReportedCost:     usage.Cost,
		}
	}
}

func (o *streamObserver) observeText(raw json.RawMessage, field string) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	var value string
	if err := json.Unmarshal(trimmed, &value); err != nil {
		if o.malformed == "" {
			o.malformed = field + " must be a string or null"
		}
		return false
	}
	return strings.TrimSpace(value) != ""
}

func (o *streamObserver) reserveToolState(size int) bool {
	if o.toolBytes+size > o.maxToolBytes {
		o.malformed = "streaming tool-call state exceeds configured limit"
		return false
	}
	o.toolBytes += size
	return true
}

func (o *streamObserver) finalize() TerminalOutcome {
	if o.malformed != "" {
		return malformedStreamOutcome(o.malformed)
	}
	if len(o.tools) > 0 {
		for _, tool := range o.tools {
			if strings.TrimSpace(tool.id) == "" || strings.TrimSpace(tool.name) == "" ||
				strings.TrimSpace(tool.arguments.String()) == "" || !json.Valid([]byte(tool.arguments.String())) {
				return malformedStreamOutcome("streaming tool call is incomplete")
			}
		}
	}
	if o.contentSeen {
		return successOutcome(TerminalSuccessContent, -1, o.finishReason)
	}
	if len(o.tools) > 0 {
		outcome := successOutcome(TerminalSuccessToolCall, -1, o.finishReason)
		outcome.ToolCallCount = len(o.tools)
		return outcome
	}
	if o.refusalSeen {
		return successOutcome(TerminalSuccessRefusal, -1, o.finishReason)
	}
	if o.reasoningSeen {
		return TerminalOutcome{
			State:        TerminalFailureReasoningOnly,
			ErrorCode:    TerminalCodeReasoningOnly,
			ChoiceIndex:  -1,
			FinishReason: o.finishReason,
			Detail:       "stream contains reasoning but no consumable artifact",
		}
	}
	return TerminalOutcome{
		State:        TerminalFailureEmpty,
		ErrorCode:    TerminalCodeEmpty,
		ChoiceIndex:  -1,
		FinishReason: o.finishReason,
		Detail:       "stream contains no consumable artifact",
	}
}

func malformedStreamOutcome(detail string) TerminalOutcome {
	return TerminalOutcome{
		State:       TerminalFailureMalformed,
		ErrorCode:   TerminalCodeMalformed,
		ChoiceIndex: -1,
		Detail:      detail,
	}
}

func (o *streamObserver) result(bytesForwarded int64, doneSeen bool) RelayResult {
	return RelayResult{
		TTFTMs:         o.ttftMs,
		FirstDataSeen:  o.firstDataSeen,
		BytesForwarded: bytesForwarded,
		ContentSeen:    o.contentSeen,
		RefusalSeen:    o.refusalSeen,
		ReasoningSeen:  o.reasoningSeen,
		FinishReason:   o.finishReason,
		UsageSeen:      o.usageSeen,
		DoneSeen:       doneSeen,
		ParseErrors:    o.parseErrors,
		Outcome:        o.finalize(),
		Usage:          o.usage,
	}
}

func writeAndFlush(dst FlushWriter, payload []byte) error {
	if _, err := dst.Write(payload); err != nil {
		return err
	}
	dst.Flush()
	return nil
}

func writeTerminalError(dst FlushWriter, outcome TerminalOutcome) error {
	payload := terminalErrorEvent(outcome)
	return writeAndFlush(dst, payload)
}

func errorEventSize(outcome TerminalOutcome) int {
	return len(terminalErrorEvent(outcome))
}

func terminalErrorEvent(outcome TerminalOutcome) []byte {
	message := "upstream returned an invalid completion"
	if outcome.State == TerminalFailureReasoningOnly {
		message = "upstream returned reasoning without a consumable completion"
	} else if outcome.State == TerminalFailureEmpty {
		message = "upstream returned an empty completion"
	}
	return protocolErrorEvent(outcome.ErrorCode, message)
}

func writeProtocolError(dst FlushWriter, code, message string) (int, error) {
	errorEvent := protocolErrorEvent(code, message)
	if err := writeAndFlush(dst, errorEvent); err != nil {
		return 0, err
	}
	doneEvent := []byte("data: [DONE]\n\n")
	if err := writeAndFlush(dst, doneEvent); err != nil {
		return len(errorEvent), err
	}
	return len(errorEvent) + len(doneEvent), nil
}

func protocolErrorEvent(code, message string) []byte {
	envelope := struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}{}
	envelope.Error.Message = message
	envelope.Error.Type = "invalid_response_error"
	envelope.Error.Code = code
	body, _ := json.Marshal(envelope)
	return append(append([]byte("data: "), body...), []byte("\n\n")...)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
