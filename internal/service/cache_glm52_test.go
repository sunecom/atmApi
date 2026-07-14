package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"atmapi/internal/glmoptimizer"
)

func TestCoalesceGLM52RouteCallsUpstreamOnceAndCachesContent(t *testing.T) {
	previousCache, previousFlights := GlobalGLM52Cache, GlobalGLM52Flights
	t.Cleanup(func() { GlobalGLM52Cache, GlobalGLM52Flights = previousCache, previousFlights })
	GlobalGLM52Cache = glmoptimizer.NewResponseCache(glmoptimizer.CacheConfig{TTL: time.Minute, MaxEntries: 10, MaxEntryBytes: 1024})
	GlobalGLM52Flights = &glmoptimizer.FlightGroup{}

	const body = `{"choices":[{"index":0,"message":{"content":"ok"},"finish_reason":"stop"}]}`
	var upstreamCalls atomic.Int32
	var sharedCalls atomic.Int32
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := 0; index < 100; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, shared, err := coalesceGLM52Route(context.Background(), "exact-key", func() (*RouteRequestResult, error) {
				upstreamCalls.Add(1)
				time.Sleep(25 * time.Millisecond)
				return &RouteRequestResult{Response: &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(body)),
				}, ChannelID: 7, ChannelName: "openrouter", ActualModel: "z-ai/glm-5.2"}, nil
			})
			if err != nil {
				t.Errorf("coalesce: %v", err)
				return
			}
			if shared {
				sharedCalls.Add(1)
			}
			got, readErr := io.ReadAll(result.Response.Body)
			if readErr != nil || string(got) != body {
				t.Errorf("body=%q err=%v", got, readErr)
			}
		}()
	}
	close(start)
	wg.Wait()
	if upstreamCalls.Load() != 1 {
		t.Fatalf("upstream called %d times", upstreamCalls.Load())
	}
	if sharedCalls.Load() != 99 {
		t.Fatalf("shared calls=%d, want 99", sharedCalls.Load())
	}
	if cached, found := GlobalGLM52Cache.Get("exact-key"); !found || string(cached) != body {
		t.Fatalf("successful response was not cached: found=%t body=%q", found, cached)
	}
}

func TestCoalesceGLM52RouteDoesNotCacheToolCall(t *testing.T) {
	previousCache, previousFlights := GlobalGLM52Cache, GlobalGLM52Flights
	t.Cleanup(func() { GlobalGLM52Cache, GlobalGLM52Flights = previousCache, previousFlights })
	GlobalGLM52Cache = glmoptimizer.NewResponseCache(glmoptimizer.CacheConfig{TTL: time.Minute})
	GlobalGLM52Flights = &glmoptimizer.FlightGroup{}
	toolCall := `{"choices":[{"index":0,"message":{"content":null,"tool_calls":[{"id":"1","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`
	_, _, err := coalesceGLM52Route(context.Background(), "tool-key", func() (*RouteRequestResult, error) {
		return &RouteRequestResult{Response: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(toolCall))}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, found := GlobalGLM52Cache.Get("tool-key"); found {
		t.Fatal("tool-call response entered local cache")
	}
}

func TestCoalesceGLM52RouteHonorsUpstreamNoStore(t *testing.T) {
	previousCache, previousFlights := GlobalGLM52Cache, GlobalGLM52Flights
	t.Cleanup(func() { GlobalGLM52Cache, GlobalGLM52Flights = previousCache, previousFlights })
	GlobalGLM52Cache = glmoptimizer.NewResponseCache(glmoptimizer.CacheConfig{TTL: time.Minute})
	GlobalGLM52Flights = &glmoptimizer.FlightGroup{}
	content := `{"choices":[{"index":0,"message":{"content":"private"},"finish_reason":"stop"}]}`
	_, _, err := coalesceGLM52Route(context.Background(), "private-key", func() (*RouteRequestResult, error) {
		return &RouteRequestResult{Response: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Cache-Control": []string{"private, no-store"}},
			Body:       io.NopCloser(strings.NewReader(content)),
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, found := GlobalGLM52Cache.Get("private-key"); found {
		t.Fatal("no-store response entered local cache")
	}
}
