package glmoptimizer

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRouterRetriesOnlyRetryableFailuresAndStopsAtTwoChannels(t *testing.T) {
	tests := []struct {
		name      string
		first     error
		wantCalls int32
		wantError bool
	}{
		{name: "400 stops", first: NewHTTPFailure(1, 400, ""), wantCalls: 1, wantError: true},
		{name: "422 stops", first: NewHTTPFailure(1, 422, ""), wantCalls: 1, wantError: true},
		{name: "401 switches", first: NewHTTPFailure(1, 401, ""), wantCalls: 2},
		{name: "402 switches", first: NewHTTPFailure(1, 402, ""), wantCalls: 2},
		{name: "403 switches", first: NewHTTPFailure(1, 403, ""), wantCalls: 2},
		{name: "429 switches", first: NewHTTPFailure(1, 429, ""), wantCalls: 2},
		{name: "500 switches", first: NewHTTPFailure(1, 500, ""), wantCalls: 2},
		{name: "network switches", first: errors.New("connection reset"), wantCalls: 2},
		{name: "semantic empty switches", first: NewTerminalFailure(1, TerminalOutcome{State: TerminalFailureEmpty, ErrorCode: TerminalCodeEmpty}), wantCalls: 2},
		{name: "cancel stops", first: context.Canceled, wantCalls: 1, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := NewRouter(RouterConfig{Backoff: -1})
			var calls atomic.Int32
			result, err := router.Route(context.Background(), glmCandidates(1, 2, 3), func(_ context.Context, candidate RouteCandidate) (AttemptResult, error) {
				call := calls.Add(1)
				if call == 1 {
					return AttemptResult{}, test.first
				}
				return AttemptResult{Value: candidate.ChannelID}, nil
			})
			if calls.Load() != test.wantCalls {
				t.Fatalf("calls = %d, want %d", calls.Load(), test.wantCalls)
			}
			if test.wantError {
				if err == nil {
					t.Fatal("Route() error = nil")
				}
				return
			}
			if err != nil || result.ChannelID != 2 || result.Attempts != 2 {
				t.Fatalf("result = %+v, error = %v", result, err)
			}
		})
	}
}

func TestRouterNeverAttemptsMoreThanTwoChannels(t *testing.T) {
	router := NewRouter(RouterConfig{MaxAttempts: 99, Backoff: -1})
	var calls atomic.Int32
	_, err := router.Route(context.Background(), glmCandidates(1, 2, 3, 4), func(_ context.Context, candidate RouteCandidate) (AttemptResult, error) {
		calls.Add(1)
		return AttemptResult{}, NewHTTPFailure(candidate.ChannelID, 503, "")
	})
	if err == nil {
		t.Fatal("Route() error = nil")
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want exactly 2", calls.Load())
	}
}

func TestRouterUsesOneTotalDeadlineAcrossAttempts(t *testing.T) {
	router := NewRouter(RouterConfig{TotalDeadline: 30 * time.Millisecond, Backoff: -1})
	started := time.Now()
	var calls atomic.Int32
	_, err := router.Route(context.Background(), glmCandidates(1, 2), func(ctx context.Context, _ RouteCandidate) (AttemptResult, error) {
		calls.Add(1)
		<-ctx.Done()
		return AttemptResult{}, ctx.Err()
	})
	if err == nil || NormalizeFailure(0, err).Class != FailureChannelTransient {
		t.Fatalf("Route() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("total deadline took %v", elapsed)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, deadline must not reset for a second channel", calls.Load())
	}
}

func TestRouterNeverRetriesAfterFirstClientByte(t *testing.T) {
	router := NewRouter(RouterConfig{Backoff: -1})
	var calls atomic.Int32
	_, err := router.Route(context.Background(), glmCandidates(1, 2), func(_ context.Context, _ RouteCandidate) (AttemptResult, error) {
		calls.Add(1)
		return AttemptResult{BytesForwarded: 1}, errors.New("stream broke")
	})
	if err == nil {
		t.Fatal("Route() error = nil")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 after bytes were committed", calls.Load())
	}
	if NormalizeFailure(0, err).BytesForwarded != 1 {
		t.Fatalf("failure = %+v", NormalizeFailure(0, err))
	}
}

func TestRouterFiltersCandidatesToExactGLM52Group(t *testing.T) {
	router := NewRouter(RouterConfig{Backoff: -1})
	candidates := []RouteCandidate{
		{ChannelID: 1, ModelGroup: "deepseek-a4"},
		{ChannelID: 2, ModelGroup: ""},
		{ChannelID: 3, ModelGroup: "glm-5.2"},
		{ChannelID: 4, ModelGroup: "GLM-5.2"},
	}
	var called []uint
	result, err := router.Route(context.Background(), candidates, func(_ context.Context, candidate RouteCandidate) (AttemptResult, error) {
		called = append(called, candidate.ChannelID)
		return AttemptResult{Value: candidate.ChannelID}, nil
	})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if len(called) != 1 || called[0] != 3 || result.ChannelID != 3 {
		t.Fatalf("called = %v, result = %+v", called, result)
	}
}

func TestRouterSkipsOpenCircuitAndUsesNextChannel(t *testing.T) {
	breaker := NewCircuitBreaker(BreakerConfig{FailureThreshold: 1, OpenDuration: time.Minute})
	permit, _ := breaker.Acquire(1)
	permit.Fail(NewHTTPFailure(1, 503, ""))
	router := NewRouter(RouterConfig{Breaker: breaker, Backoff: -1})
	var calls []uint
	result, err := router.Route(context.Background(), glmCandidates(1, 2), func(_ context.Context, candidate RouteCandidate) (AttemptResult, error) {
		calls = append(calls, candidate.ChannelID)
		return AttemptResult{Value: candidate.ChannelID}, nil
	})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if len(calls) != 1 || calls[0] != 2 || result.ChannelID != 2 {
		t.Fatalf("calls = %v, result = %+v", calls, result)
	}
}

func TestRouterKeepsHalfOpenProbeUntilDeferredStreamOutcome(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	breaker := NewCircuitBreaker(BreakerConfig{FailureThreshold: 1, OpenDuration: time.Minute, Now: clock.Now})
	permit, _ := breaker.Acquire(1)
	permit.Fail(NewHTTPFailure(1, 503, ""))
	clock.Advance(time.Minute)
	router := NewRouter(RouterConfig{Breaker: breaker, Backoff: -1})

	result, err := router.Route(context.Background(), glmCandidates(1), func(_ context.Context, _ RouteCandidate) (AttemptResult, error) {
		return AttemptResult{Value: "stream", DeferredOutcome: true}, nil
	})
	if err != nil || result.Completion == nil {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
	if state := breaker.State(1); state != BreakerHalfOpen {
		t.Fatalf("state = %s, want half-open until stream terminal", state)
	}
	if _, ok := breaker.Acquire(1); ok {
		t.Fatal("a second half-open request was allowed while stream outcome was pending")
	}
	result.Completion.Succeed()
	if state := breaker.State(1); state != BreakerClosed {
		t.Fatalf("state = %s, want closed after valid stream terminal", state)
	}
}

func glmCandidates(ids ...uint) []RouteCandidate {
	result := make([]RouteCandidate, 0, len(ids))
	for _, id := range ids {
		result = append(result, RouteCandidate{ChannelID: id, ModelGroup: ModelGLM52})
	}
	return result
}
