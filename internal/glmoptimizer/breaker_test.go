package glmoptimizer

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

func TestCircuitBreakerSlidingWindowAndOpen(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	breaker := NewCircuitBreaker(BreakerConfig{
		FailureThreshold: 3,
		FailureWindow:    time.Minute,
		OpenDuration:     time.Minute,
		Now:              clock.Now,
	})

	for attempt := 0; attempt < 3; attempt++ {
		permit, ok := breaker.Acquire(11)
		if !ok {
			t.Fatalf("attempt %d was unexpectedly blocked", attempt+1)
		}
		permit.Fail(NewHTTPFailure(11, 503, ""))
	}
	if state := breaker.State(11); state != BreakerOpen {
		t.Fatalf("state = %s, want open", state)
	}
	if _, ok := breaker.Acquire(11); ok {
		t.Fatal("open breaker allowed a request before cooldown")
	}
}

func TestCircuitBreakerAllowsOnlyOneHalfOpenProbe(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	breaker := NewCircuitBreaker(BreakerConfig{FailureThreshold: 1, FailureWindow: time.Minute, OpenDuration: time.Minute, Now: clock.Now})
	permit, _ := breaker.Acquire(7)
	permit.Fail(NewHTTPFailure(7, 429, ""))
	clock.Advance(time.Minute)

	var allowed atomic.Int32
	var probe *CircuitPermit
	var probeMu sync.Mutex
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			candidate, ok := breaker.Acquire(7)
			if ok {
				allowed.Add(1)
				probeMu.Lock()
				probe = candidate
				probeMu.Unlock()
			}
		}()
	}
	group.Wait()
	if got := allowed.Load(); got != 1 {
		t.Fatalf("half-open probes = %d, want 1", got)
	}
	probe.Succeed()
	if state := breaker.State(7); state != BreakerClosed {
		t.Fatalf("state = %s, want closed after successful probe", state)
	}
	if next, ok := breaker.Acquire(7); !ok {
		t.Fatal("closed breaker rejected request")
	} else {
		next.Succeed()
	}
}

func TestCircuitBreakerHalfOpenFailureReopens(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	breaker := NewCircuitBreaker(BreakerConfig{FailureThreshold: 1, FailureWindow: time.Minute, OpenDuration: time.Minute, Now: clock.Now})
	permit, _ := breaker.Acquire(5)
	permit.Fail(NewHTTPFailure(5, 500, ""))
	clock.Advance(time.Minute)
	probe, ok := breaker.Acquire(5)
	if !ok {
		t.Fatal("half-open probe was not allowed")
	}
	probe.Fail(NewHTTPFailure(5, 503, ""))
	if state := breaker.State(5); state != BreakerOpen {
		t.Fatalf("state = %s, want reopened", state)
	}
}

func TestCircuitBreakerDoesNotCountPermanentOrSemanticFailures(t *testing.T) {
	breaker := NewCircuitBreaker(BreakerConfig{FailureThreshold: 1})
	statuses := []int{400, 401, 402, 403, 422}
	for _, status := range statuses {
		permit, ok := breaker.Acquire(uint(status))
		if !ok {
			t.Fatalf("status %d unexpectedly blocked", status)
		}
		permit.Fail(NewHTTPFailure(uint(status), status, ""))
		if state := breaker.State(uint(status)); state != BreakerClosed {
			t.Fatalf("status %d state = %s, want closed", status, state)
		}
	}
	permit, _ := breaker.Acquire(99)
	permit.Fail(NewTerminalFailure(99, TerminalOutcome{State: TerminalFailureEmpty, ErrorCode: TerminalCodeEmpty}))
	if state := breaker.State(99); state != BreakerClosed {
		t.Fatalf("semantic failure state = %s, want closed", state)
	}
}

func TestCircuitBreakerSuccessDoesNotEraseClosedWindowHistory(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	breaker := NewCircuitBreaker(BreakerConfig{FailureThreshold: 3, FailureWindow: time.Minute, OpenDuration: time.Minute, Now: clock.Now})
	first, _ := breaker.Acquire(3)
	first.Fail(NewHTTPFailure(3, 500, ""))
	success, _ := breaker.Acquire(3)
	success.Succeed()
	second, _ := breaker.Acquire(3)
	second.Fail(NewHTTPFailure(3, 500, ""))
	third, _ := breaker.Acquire(3)
	third.Fail(NewHTTPFailure(3, 500, ""))
	if state := breaker.State(3); state != BreakerOpen {
		t.Fatalf("state = %s, want open; a normal success must not erase the whole failure window", state)
	}
}

func TestCircuitBreakerPrunesExpiredFailures(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	breaker := NewCircuitBreaker(BreakerConfig{FailureThreshold: 2, FailureWindow: time.Minute, OpenDuration: time.Minute, Now: clock.Now})
	first, _ := breaker.Acquire(8)
	first.Fail(NewHTTPFailure(8, 500, ""))
	clock.Advance(time.Minute + time.Second)
	second, _ := breaker.Acquire(8)
	second.Fail(NewHTTPFailure(8, 500, ""))
	if state := breaker.State(8); state != BreakerClosed {
		t.Fatalf("state = %s, want closed after old failure expired", state)
	}
}

func TestCircuitBreakerHonorsRetryAfterBeforeThreshold(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	breaker := NewCircuitBreaker(BreakerConfig{FailureThreshold: 3, FailureWindow: time.Minute, OpenDuration: time.Minute, Now: clock.Now})
	permit, _ := breaker.Acquire(12)
	permit.Fail(NewHTTPFailure(12, 429, "10"))
	if state := breaker.State(12); state != BreakerClosed {
		t.Fatalf("state = %s, a single 429 must not trip the threshold breaker", state)
	}
	if _, ok := breaker.Acquire(12); ok {
		t.Fatal("channel was allowed inside Retry-After window")
	}
	clock.Advance(10 * time.Second)
	if next, ok := breaker.Acquire(12); !ok {
		t.Fatal("channel stayed blocked after Retry-After elapsed")
	} else {
		next.Succeed()
	}
}
