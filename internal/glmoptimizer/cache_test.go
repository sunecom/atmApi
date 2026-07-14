package glmoptimizer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionAndCacheKeysArePseudonymousAndScoped(t *testing.T) {
	secret := []byte("top-secret-token")
	session := DeriveSessionID(secret, "tenant-7", "user-42")
	if strings.Contains(session, "top-secret") || strings.Contains(session, "user-42") {
		t.Fatalf("session leaks source identity: %q", session)
	}
	if session == DeriveSessionID(secret, "tenant-8", "user-42") {
		t.Fatal("session must be tenant scoped")
	}
	body, err := InjectStableSessionID([]byte(`{"model":"glm-5.2","session_id":"raw-user"}`), secret, "tenant-7", "")
	if err != nil || strings.Contains(string(body), "raw-user") {
		t.Fatalf("raw session identifier leaked: %s, %v", body, err)
	}
	key := BuildCacheKey(secret, "tenant-7", CachePolicyVersion, body)
	if strings.Contains(key, "top-secret") || strings.Contains(key, "tenant-7") {
		t.Fatalf("cache key leaks source material: %q", key)
	}
}

func TestCacheEligibility(t *testing.T) {
	eligible := `{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}],"temperature":0,"n":1,"reasoning":{"effort":"high"}}`
	if decision := EvaluateCacheEligibility([]byte(eligible)); !decision.Eligible {
		t.Fatalf("expected eligible: %+v", decision)
	}
	cases := map[string]string{
		"missing temperature": `{"model":"glm-5.2","messages":[]}`,
		"nonzero temperature": `{"model":"glm-5.2","messages":[],"temperature":0.1}`,
		"stream":              `{"model":"glm-5.2","messages":[],"temperature":0,"stream":true}`,
		"tools":               `{"model":"glm-5.2","messages":[],"temperature":0,"tools":[]}`,
		"tool history":        `{"model":"glm-5.2","messages":[{"role":"tool","tool_call_id":"x","content":"done"}],"temperature":0}`,
		"multimodal":          `{"model":"glm-5.2","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"temperature":0}`,
		"unknown behavior":    `{"model":"glm-5.2","messages":[],"temperature":0,"frequency_penalty":1}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if decision := EvaluateCacheEligibility([]byte(body)); decision.Eligible {
				t.Fatalf("expected bypass: %+v", decision)
			}
		})
	}
}

func TestCacheKeyCoversBehaviorFields(t *testing.T) {
	base := `{"model":"glm-5.2","messages":[],"temperature":0,"%s":%s}`
	variants := []struct{ field, value string }{
		{"tools", `[{"type":"function"}]`}, {"reasoning", `{"effort":"high"}`},
		{"response_format", `{"type":"json_object"}`}, {"top_p", `0.5`},
		{"stop", `["END"]`}, {"seed", `7`},
	}
	secret := []byte("secret")
	seen := map[string]bool{}
	for _, variant := range variants {
		canonical, err := CanonicalizeJSON([]byte(fmt.Sprintf(base, variant.field, variant.value)))
		if err != nil {
			t.Fatal(err)
		}
		key := BuildCacheKey(secret, "tenant", CachePolicyVersion, canonical)
		if seen[key] {
			t.Fatalf("field %s did not change cache key", variant.field)
		}
		seen[key] = true
	}
}

func TestResponseCacheCopiesAndAdmitsOnlyCompleteContent(t *testing.T) {
	cache := NewResponseCache(CacheConfig{TTL: time.Minute, MaxEntries: 2, MaxEntryBytes: 1024})
	valid := []byte(`{"choices":[{"index":0,"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	if !cache.Set("a", valid) {
		t.Fatal("valid response not admitted")
	}
	valid[0] = 'x'
	got, found := cache.Get("a")
	if !found || got[0] != '{' {
		t.Fatal("cache did not copy input")
	}
	got[0] = 'x'
	again, _ := cache.Get("a")
	if again[0] != '{' {
		t.Fatal("cache did not copy output")
	}
	if cache.Set("bad", []byte(`{"choices":[]}`)) {
		t.Fatal("empty completion admitted")
	}
	mixedToolCall := []byte(`{"choices":[{"index":0,"message":{"content":"ok","tool_calls":[{"id":"1","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
	if cache.Set("mixed", mixedToolCall) {
		t.Fatal("content response carrying a tool call was admitted")
	}
	if cache.Set("large", make([]byte, 1025)) {
		t.Fatal("oversized response admitted")
	}
}

func TestResponseCacheEnforcesTotalByteLimit(t *testing.T) {
	responseA := []byte(`{"choices":[{"index":0,"message":{"content":"aaaaaaaaaa"},"finish_reason":"stop"}]}`)
	responseB := []byte(`{"choices":[{"index":0,"message":{"content":"bbbbbbbbbb"},"finish_reason":"stop"}]}`)
	cache := NewResponseCache(CacheConfig{TTL: time.Minute, MaxEntries: 10, MaxEntryBytes: 1024, MaxTotalBytes: len(responseA) + len(responseB) - 1})
	if !cache.Set("a", responseA) || !cache.Set("b", responseB) {
		t.Fatal("valid response rejected")
	}
	if _, found := cache.Get("a"); found {
		t.Fatal("oldest response was not evicted by total-byte limit")
	}
	if _, found := cache.Get("b"); !found {
		t.Fatal("newest response was unexpectedly evicted")
	}
}

func TestFlightGroupCoalescesIdenticalCalls(t *testing.T) {
	var group FlightGroup
	var calls atomic.Int32
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			value, _, err := group.Do(context.Background(), "same", func() ([]byte, error) {
				calls.Add(1)
				time.Sleep(20 * time.Millisecond)
				return []byte("ok"), nil
			})
			if err != nil || string(value) != "ok" {
				t.Errorf("unexpected result %q, %v", value, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("upstream called %d times", calls.Load())
	}
}
