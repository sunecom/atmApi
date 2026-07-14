package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"atmapi/internal/glmoptimizer"
	"atmapi/internal/model"
)

func TestTryGLM52ChannelReturnsConsumableNonStreamResponse(t *testing.T) {
	var receivedModel string
	var receivedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		receivedBody, _ = io.ReadAll(request.Body)
		var body struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(receivedBody, &body)
		receivedModel = body.Model
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"choices":[{"index":0,"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	channel := model.Channel{
		ID: 7, Name: "test", Status: 1, ModelGroup: glmoptimizer.ModelGLM52,
		BaseURL: server.URL, ModelMapping: `{"glm-5.2":"z-ai/glm-5.2"}`,
	}
	response, actualModel, err := tryGLM52Channel(context.Background(), channel, glmoptimizer.ModelGLM52,
		[]byte(`{"seed":9007199254740993,"messages":[{"content":"hi","role":"user"}],"model":"glm-5.2"}`), false)
	if err != nil {
		t.Fatalf("tryGLM52Channel() error = %v", err)
	}
	defer response.Body.Close()
	if actualModel != "z-ai/glm-5.2" || receivedModel != "z-ai/glm-5.2" {
		t.Fatalf("actual = %q, received = %q", actualModel, receivedModel)
	}
	expected, _ := glmoptimizer.CanonicalizeJSON([]byte(`{"model":"z-ai/glm-5.2","messages":[{"role":"user","content":"hi"}],"seed":9007199254740993}`))
	if string(receivedBody) != string(expected) {
		t.Fatalf("upstream body is not the exact canonical request:\n%s\nwant:\n%s", receivedBody, expected)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), `"content":"ok"`) {
		t.Fatalf("response body = %s", body)
	}
}

func TestTryGLM52ChannelClassifiesReasoningOnlyForRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, `{"choices":[{"index":0,"message":{"content":"","reasoning":"private"},"finish_reason":"length"}]}`)
	}))
	defer server.Close()
	channel := model.Channel{ID: 8, Status: 1, ModelGroup: glmoptimizer.ModelGLM52, BaseURL: server.URL}

	_, _, err := tryGLM52Channel(context.Background(), channel, glmoptimizer.ModelGLM52,
		[]byte(`{"model":"glm-5.2","messages":[]}`), false)
	failure := glmoptimizer.NormalizeFailure(channel.ID, err)
	if failure.Class != glmoptimizer.FailureSemanticEmpty || !failure.Retryable() || failure.CountsTowardBreaker() {
		t.Fatalf("failure = %+v", failure)
	}
}

func TestTryGLM52ChannelUsesTypedHTTPFailureWithoutLeakingBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Retry-After", "9")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(writer, `{"error":"secret upstream diagnostic"}`)
	}))
	defer server.Close()
	channel := model.Channel{ID: 9, Status: 1, ModelGroup: glmoptimizer.ModelGLM52, BaseURL: server.URL}

	_, _, err := tryGLM52Channel(context.Background(), channel, glmoptimizer.ModelGLM52,
		[]byte(`{"model":"glm-5.2","messages":[]}`), false)
	failure := glmoptimizer.NormalizeFailure(channel.ID, err)
	if failure.Class != glmoptimizer.FailureChannelRateLimit || failure.RetryAfter.Seconds() != 9 {
		t.Fatalf("failure = %+v", failure)
	}
	if strings.Contains(failure.Error(), "secret upstream") {
		t.Fatalf("typed failure leaked upstream body: %s", failure.Error())
	}
}

func TestReleaseOnCloseBodyReleasesConcurrencyExactlyOnce(t *testing.T) {
	var releases atomic.Int32
	body := &releaseOnCloseBody{
		ReadCloser: io.NopCloser(strings.NewReader("stream")),
		release:    func() { releases.Add(1) },
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if releases.Load() != 1 {
		t.Fatalf("releases = %d, want 1", releases.Load())
	}
}
