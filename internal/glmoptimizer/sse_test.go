package glmoptimizer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingFlushWriter struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	flushes chan string
}

func newRecordingFlushWriter() *recordingFlushWriter {
	return &recordingFlushWriter{flushes: make(chan string, 16)}
}

func (w *recordingFlushWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *recordingFlushWriter) Flush() {
	w.mu.Lock()
	snapshot := w.buf.String()
	w.mu.Unlock()
	w.flushes <- snapshot
}

func (w *recordingFlushWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestRelaySSEForwardsFirstCompleteEventImmediately(t *testing.T) {
	reader, writer := io.Pipe()
	destination := newRecordingFlushWriter()
	done := make(chan error, 1)

	go func() {
		_, err := RelaySSE(context.Background(), destination, reader, RelayOptions{})
		done <- err
	}()

	first := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n"
	if _, err := io.WriteString(writer, first); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-destination.flushes:
		if got != first {
			t.Fatalf("first flush = %q, want the first complete event only", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first event was buffered instead of being flushed immediately")
	}

	if _, err := io.WriteString(writer, "data: [DONE]\n\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("RelaySSE() error = %v", err)
	}
}

func TestRelaySSEPreservesEventBoundariesAndObservesMetadata(t *testing.T) {
	upstream := strings.Join([]string{
		": heartbeat\r\n\r\n",
		"event: message\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"lookup\",\"arguments\":\"\"}}]}}]}\ndata: \n\n",
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"q\\\":\\\"glm\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":4,\"total_tokens\":24,\"cost\":0.00125,\"prompt_tokens_details\":{\"cached_tokens\":12,\"cache_write_tokens\":3},\"completion_tokens_details\":{\"reasoning_tokens\":2}}}\n\n",
		"data: [DONE]\n\n",
	}, "")
	destination := newRecordingFlushWriter()

	result, err := RelaySSE(context.Background(), destination, strings.NewReader(upstream), RelayOptions{})
	if err != nil {
		t.Fatalf("RelaySSE() error = %v", err)
	}
	if !result.DoneSeen || result.ParseErrors != 0 {
		t.Fatalf("result = %+v", result)
	}
	if !result.FirstDataSeen {
		t.Fatalf("first data event was not observed: %+v", result)
	}
}

func TestRelaySSEReplacesDoneAfterInvalidTerminal(t *testing.T) {
	stream := "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning\":\"private\"},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n"
	destination := newRecordingFlushWriter()

	result, err := RelaySSE(context.Background(), destination, strings.NewReader(stream), RelayOptions{})
	if err != nil {
		t.Fatalf("RelaySSE() error = %v", err)
	}
	if result.Outcome.State != TerminalFailureReasoningOnly {
		t.Fatalf("outcome = %+v", result.Outcome)
	}
	got := destination.String()
	if strings.Count(got, "data: [DONE]") != 1 || !strings.Contains(got, TerminalCodeReasoningOnly) {
		t.Fatalf("invalid terminal was not replaced by error + one DONE: %q", got)
	}
	if strings.Contains(got, "private") == false {
		t.Fatal("already-forwarded reasoning event was unexpectedly rewritten")
	}
}

func TestRelaySSEReportsMidStreamEOF(t *testing.T) {
	stream := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n"
	destination := newRecordingFlushWriter()

	result, err := RelaySSE(context.Background(), destination, strings.NewReader(stream), RelayOptions{})
	if !errors.Is(err, ErrStreamInterrupted) {
		t.Fatalf("RelaySSE() error = %v, want ErrStreamInterrupted", err)
	}
	if result.DoneSeen {
		t.Fatal("interrupted stream must not be reported as an upstream DONE")
	}
	got := destination.String()
	if !strings.Contains(got, TerminalCodeStreamInterrupted) || !strings.HasSuffix(got, "data: [DONE]\n\n") {
		t.Fatalf("interrupted stream error is incomplete: %q", got)
	}
}

func TestRelaySSEStopsOnClientCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	destination := newRecordingFlushWriter()

	_, err := RelaySSE(ctx, destination, strings.NewReader("data: [DONE]\n\n"), RelayOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RelaySSE() error = %v, want context.Canceled", err)
	}
	if destination.String() != "" {
		t.Fatalf("canceled client received bytes: %q", destination.String())
	}
}

func TestRelaySSECancellationUnblocksUpstreamRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader, _ := io.Pipe()
	destination := newRecordingFlushWriter()
	done := make(chan error, 1)
	go func() {
		_, err := RelaySSE(ctx, destination, reader, RelayOptions{})
		done <- err
	}()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RelaySSE() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("client cancellation did not unblock the upstream read")
	}
}

func TestStreamObserverBoundsCumulativeToolState(t *testing.T) {
	observer := newStreamObserver(96)
	observer.observe([]byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":"12345678901234567890"}}]}}]}`))
	observer.observe([]byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"12345678901234567890"}}]}}]}`))

	outcome := observer.finalize()
	if outcome.State != TerminalFailureMalformed || !strings.Contains(outcome.Detail, "exceeds configured limit") {
		t.Fatalf("outcome = %+v, want bounded-state malformed result", outcome)
	}
}

func TestRelaySSERejectsMalformedContentTypeAtDone(t *testing.T) {
	stream := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":42}}]}\n\ndata: [DONE]\n\n"
	destination := newRecordingFlushWriter()

	result, err := RelaySSE(context.Background(), destination, strings.NewReader(stream), RelayOptions{})
	if err != nil {
		t.Fatalf("RelaySSE() error = %v", err)
	}
	if result.Outcome.State != TerminalFailureMalformed || !strings.Contains(destination.String(), TerminalCodeMalformed) {
		t.Fatalf("result = %+v, output = %q", result, destination.String())
	}
}

func TestRelaySSEDoesNotHideIncompleteToolCallBehindContent(t *testing.T) {
	stream := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"visible\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"lookup\",\"arguments\":\"\"}}]}}]}\n\ndata: [DONE]\n\n"
	destination := newRecordingFlushWriter()

	result, err := RelaySSE(context.Background(), destination, strings.NewReader(stream), RelayOptions{})
	if err != nil {
		t.Fatalf("RelaySSE() error = %v", err)
	}
	if result.Outcome.State != TerminalFailureMalformed {
		t.Fatalf("result = %+v, incomplete tool call must remain malformed", result)
	}
}

func TestRelaySSEBoundsSingleEventMemory(t *testing.T) {
	destination := newRecordingFlushWriter()
	stream := "data: " + strings.Repeat("x", 65) + "\n\n"

	_, err := RelaySSE(context.Background(), destination, strings.NewReader(stream), RelayOptions{MaxEventBytes: 64})
	if !errors.Is(err, ErrSSEEventTooLarge) {
		t.Fatalf("RelaySSE() error = %v, want ErrSSEEventTooLarge", err)
	}
	if destination.String() != "" {
		t.Fatalf("oversized event was partially forwarded: %q", destination.String())
	}
}

func TestRelaySSEProtocolFixtures(t *testing.T) {
	tests := []struct {
		name  string
		state TerminalState
	}{
		{name: "content", state: TerminalSuccessContent},
		{name: "tool_call", state: TerminalSuccessToolCall},
		{name: "refusal", state: TerminalSuccessRefusal},
		{name: "reasoning_only", state: TerminalFailureReasoningOnly},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stream, err := os.ReadFile("testdata/stream/" + test.name + ".sse")
			if err != nil {
				t.Fatal(err)
			}
			destination := newRecordingFlushWriter()
			result, err := RelaySSE(context.Background(), destination, bytes.NewReader(stream), RelayOptions{})
			if err != nil {
				t.Fatalf("RelaySSE() error = %v", err)
			}
			if result.Outcome.State != test.state || !result.DoneSeen {
				t.Fatalf("result = %+v", result)
			}
			if test.state == TerminalFailureReasoningOnly {
				if !strings.Contains(destination.String(), TerminalCodeReasoningOnly) {
					t.Fatalf("reasoning-only fixture lacks terminal error: %q", destination.String())
				}
			} else if destination.String() != string(stream) {
				t.Fatal("valid fixture was not relayed byte-for-byte")
			}
		})
	}
}
