package glmoptimizer

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	DefaultRouteDeadline = 90 * time.Second
	DefaultRouteBackoff  = 50 * time.Millisecond
)

var ErrNoEligibleChannel = errors.New("no eligible GLM-5.2 channel")

type RouteCandidate struct {
	ChannelID  uint
	ModelGroup string
}

type AttemptResult struct {
	Value           any
	BytesForwarded  int64
	DeferredOutcome bool
}

type RouteResult struct {
	Value      any
	ChannelID  uint
	Attempts   int
	Completion *RouteCompletion
}

// RouteCompletion keeps a half-open probe reserved until a streaming caller
// has observed the terminal event. Non-streaming attempts complete inline.
type RouteCompletion struct {
	permit    *CircuitPermit
	channelID uint
}

func (c *RouteCompletion) Succeed() {
	if c != nil {
		c.permit.Succeed()
	}
}

func (c *RouteCompletion) Fail(err error, bytesForwarded int64) {
	if c == nil {
		return
	}
	failure := NormalizeFailure(c.channelID, err)
	if bytesForwarded > failure.BytesForwarded {
		failure.BytesForwarded = bytesForwarded
	}
	c.permit.Fail(failure)
}

type AttemptFunc func(context.Context, RouteCandidate) (AttemptResult, error)

type RouterConfig struct {
	MaxAttempts   int
	TotalDeadline time.Duration
	Backoff       time.Duration
	RequiredGroup string
	Breaker       *CircuitBreaker
}

// Router applies the GLM-5.2 retry boundary independently from legacy model
// routing: exact model-group membership, one total deadline, and at most two
// actual channel attempts.
type Router struct {
	maxAttempts   int
	totalDeadline time.Duration
	backoff       time.Duration
	requiredGroup string
	breaker       *CircuitBreaker
}

func NewRouter(config RouterConfig) *Router {
	maxAttempts := config.MaxAttempts
	if maxAttempts <= 0 || maxAttempts > 2 {
		maxAttempts = 2
	}
	deadline := config.TotalDeadline
	if deadline <= 0 {
		deadline = DefaultRouteDeadline
	}
	backoff := config.Backoff
	if backoff == 0 {
		backoff = DefaultRouteBackoff
	} else if backoff < 0 {
		backoff = 0
	}
	requiredGroup := strings.TrimSpace(config.RequiredGroup)
	if requiredGroup == "" {
		requiredGroup = ModelGLM52
	}
	breaker := config.Breaker
	if breaker == nil {
		breaker = NewCircuitBreaker(BreakerConfig{})
	}
	return &Router{
		maxAttempts:   maxAttempts,
		totalDeadline: deadline,
		backoff:       backoff,
		requiredGroup: requiredGroup,
		breaker:       breaker,
	}
}

func (r *Router) Route(ctx context.Context, candidates []RouteCandidate, attempt AttemptFunc) (RouteResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	routeCtx, cancel := context.WithTimeout(ctx, r.totalDeadline)
	defer cancel()

	var lastFailure *Failure
	attempts := 0
	for _, candidate := range candidates {
		if !strings.EqualFold(strings.TrimSpace(candidate.ModelGroup), r.requiredGroup) {
			continue
		}
		if attempts >= r.maxAttempts {
			break
		}
		if err := routeCtx.Err(); err != nil {
			return RouteResult{Attempts: attempts}, NormalizeFailure(candidate.ChannelID, err)
		}

		permit, allowed := r.breaker.Acquire(candidate.ChannelID)
		if !allowed {
			continue
		}
		attempts++
		attemptResult, err := attempt(routeCtx, candidate)
		if err == nil {
			result := RouteResult{Value: attemptResult.Value, ChannelID: candidate.ChannelID, Attempts: attempts}
			if attemptResult.DeferredOutcome {
				result.Completion = &RouteCompletion{permit: permit, channelID: candidate.ChannelID}
			} else {
				permit.Succeed()
			}
			return result, nil
		}

		failure := NormalizeFailure(candidate.ChannelID, err)
		if attemptResult.BytesForwarded > failure.BytesForwarded {
			failure.BytesForwarded = attemptResult.BytesForwarded
		}
		permit.Fail(failure)
		lastFailure = failure
		if !failure.Retryable() {
			return RouteResult{Attempts: attempts}, failure
		}
		if routeCtx.Err() != nil {
			return RouteResult{Attempts: attempts}, NormalizeFailure(candidate.ChannelID, routeCtx.Err())
		}
		if attempts < r.maxAttempts && r.backoff > 0 {
			if err := waitForRouteBackoff(routeCtx, r.backoff*time.Duration(1<<(attempts-1))); err != nil {
				return RouteResult{Attempts: attempts}, NormalizeFailure(candidate.ChannelID, err)
			}
		}
	}

	if lastFailure != nil {
		return RouteResult{Attempts: attempts}, lastFailure
	}
	return RouteResult{Attempts: attempts}, &Failure{Class: FailureChannelTransient, Cause: ErrNoEligibleChannel}
}

func waitForRouteBackoff(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
