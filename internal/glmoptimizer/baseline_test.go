package glmoptimizer

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// legacyHasVisibleContent mirrors the emergency implementation's string-based
// success check. It intentionally exists only as a characterization helper.
func legacyHasVisibleContent(body []byte) bool {
	text := string(body)
	return strings.Contains(text, `"content":`) &&
		!strings.Contains(text, `"content":""`) &&
		!strings.Contains(text, `"content":null`)
}

// legacyBufferedRelay mirrors the emergency implementation's all-or-nothing
// buffering behavior. Production code must not call this helper.
func legacyBufferedRelay(dst io.Writer, src io.Reader) error {
	body, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	_, err = dst.Write(body)
	return err
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Len()
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func fixture(t *testing.T, parts ...string) []byte {
	t.Helper()
	path := filepath.Join(append([]string{"testdata"}, parts...)...)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return body
}

func TestBaselineLegacyStringCheckRejectsValidToolCall(t *testing.T) {
	body := fixture(t, "nonstream", "tool_call.json")
	if !json.Valid(body) {
		t.Fatal("tool-call fixture must be valid JSON")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, body); err != nil {
		t.Fatalf("compact tool-call fixture: %v", err)
	}

	var response struct {
		Choices []struct {
			Message struct {
				ToolCalls []json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode tool-call fixture: %v", err)
	}
	if len(response.Choices) != 1 || len(response.Choices[0].Message.ToolCalls) != 1 {
		t.Fatal("fixture must contain one valid tool call")
	}
	if legacyHasVisibleContent(compact.Bytes()) {
		t.Fatal("legacy string check unexpectedly accepted content:null tool call")
	}
	t.Log("characterized bug: a valid tool call is classified as empty by the legacy string check")
}

func TestBaselineLegacyBufferingDelaysFirstSSEEvent(t *testing.T) {
	upstreamReader, upstreamWriter := io.Pipe()
	downstream := &lockedBuffer{}
	relayDone := make(chan error, 1)
	firstEventWritten := make(chan struct{})
	releaseUpstream := make(chan struct{})

	go func() {
		relayDone <- legacyBufferedRelay(downstream, upstreamReader)
	}()
	go func() {
		_, _ = io.WriteString(upstreamWriter, "data: {\"choices\":[{\"delta\":{\"reasoning\":\"Thinking\"}}]}\n\n")
		close(firstEventWritten)
		<-releaseUpstream
		_, _ = io.WriteString(upstreamWriter, "data: [DONE]\n\n")
		_ = upstreamWriter.Close()
	}()

	select {
	case <-firstEventWritten:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not produce the first SSE event")
	}
	if got := downstream.Len(); got != 0 {
		t.Fatalf("legacy relay forwarded %d bytes before upstream EOF; expected zero", got)
	}

	close(releaseUpstream)
	select {
	case err := <-relayDone:
		if err != nil {
			t.Fatalf("legacy relay failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("legacy relay did not finish after upstream EOF")
	}
	if got := downstream.String(); !strings.Contains(got, "[DONE]") {
		t.Fatalf("legacy relay did not eventually copy the full stream: %q", got)
	}
	t.Log("characterized bug: no SSE bytes reach the client until the upstream closes")
}

func TestProtocolFixtureInventory(t *testing.T) {
	validJSON := []string{
		"content.json",
		"reasoning_only.json",
		"tool_call.json",
		"refusal.json",
		"empty_choices.json",
	}
	for _, name := range validJSON {
		t.Run(name, func(t *testing.T) {
			if body := fixture(t, "nonstream", name); !json.Valid(body) {
				t.Fatalf("fixture %s must be valid JSON", name)
			}
		})
	}
	if body := fixture(t, "nonstream", "malformed.json"); json.Valid(body) {
		t.Fatal("malformed fixture must stay invalid")
	}

	completeStreams := []string{"content.sse", "reasoning_only.sse", "tool_call.sse", "refusal.sse"}
	for _, name := range completeStreams {
		t.Run(name, func(t *testing.T) {
			if body := string(fixture(t, "stream", name)); !strings.Contains(body, "data: [DONE]") {
				t.Fatalf("complete stream fixture %s must contain [DONE]", name)
			}
		})
	}
	if body := string(fixture(t, "stream", "interrupted.sse")); strings.Contains(body, "data: [DONE]") {
		t.Fatal("interrupted stream fixture must not contain [DONE]")
	}
}
