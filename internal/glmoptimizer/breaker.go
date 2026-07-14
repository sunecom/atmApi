package glmoptimizer

import (
	"sync"
	"time"
)

type BreakerState string

const (
	BreakerClosed   BreakerState = "closed"
	BreakerOpen     BreakerState = "open"
	BreakerHalfOpen BreakerState = "half_open"
)

type BreakerConfig struct {
	FailureThreshold int
	FailureWindow    time.Duration
	OpenDuration     time.Duration
	Now              func() time.Time
}

type breakerChannelState struct {
	state         BreakerState
	failures      []time.Time
	openUntil     time.Time
	blockedUntil  time.Time
	probeInFlight bool
}

// CircuitBreaker tracks transient health independently for each channel ID.
// Channel names are intentionally absent because they are mutable labels.
type CircuitBreaker struct {
	mu               sync.Mutex
	channels         map[uint]*breakerChannelState
	failureThreshold int
	failureWindow    time.Duration
	openDuration     time.Duration
	now              func() time.Time
}

func NewCircuitBreaker(config BreakerConfig) *CircuitBreaker {
	if config.FailureThreshold <= 0 {
		config.FailureThreshold = 3
	}
	if config.FailureWindow <= 0 {
		config.FailureWindow = time.Minute
	}
	if config.OpenDuration <= 0 {
		config.OpenDuration = time.Minute
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &CircuitBreaker{
		channels:         make(map[uint]*breakerChannelState),
		failureThreshold: config.FailureThreshold,
		failureWindow:    config.FailureWindow,
		openDuration:     config.OpenDuration,
		now:              config.Now,
	}
}

// Acquire returns a permit when the channel may be attempted. Once an open
// interval expires, exactly one caller receives a half-open probe permit.
func (b *CircuitBreaker) Acquire(channelID uint) (*CircuitPermit, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	state := b.channel(channelID)
	b.prune(state, now)
	if now.Before(state.blockedUntil) {
		return nil, false
	}

	switch state.state {
	case BreakerOpen:
		if now.Before(state.openUntil) {
			return nil, false
		}
		state.state = BreakerHalfOpen
		state.probeInFlight = true
		return &CircuitPermit{breaker: b, channelID: channelID, halfOpen: true}, true
	case BreakerHalfOpen:
		return nil, false
	default:
		return &CircuitPermit{breaker: b, channelID: channelID}, true
	}
}

func (b *CircuitBreaker) State(channelID uint) BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	state, ok := b.channels[channelID]
	if !ok {
		return BreakerClosed
	}
	return state.state
}

func (b *CircuitBreaker) channel(channelID uint) *breakerChannelState {
	state := b.channels[channelID]
	if state == nil {
		state = &breakerChannelState{state: BreakerClosed}
		b.channels[channelID] = state
	}
	return state
}

func (b *CircuitBreaker) prune(state *breakerChannelState, now time.Time) {
	cutoff := now.Add(-b.failureWindow)
	firstValid := 0
	for firstValid < len(state.failures) && state.failures[firstValid].Before(cutoff) {
		firstValid++
	}
	if firstValid > 0 {
		state.failures = append([]time.Time(nil), state.failures[firstValid:]...)
	}
}

func (b *CircuitBreaker) succeed(channelID uint, halfOpen bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.channel(channelID)
	if halfOpen && state.state == BreakerHalfOpen {
		state.state = BreakerClosed
		state.failures = nil
		state.openUntil = time.Time{}
		state.probeInFlight = false
	}
}

func (b *CircuitBreaker) fail(channelID uint, halfOpen bool, failure *Failure) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.channel(channelID)
	if failure != nil && failure.RetryAfter > 0 {
		blockedUntil := b.now().Add(failure.RetryAfter)
		if blockedUntil.After(state.blockedUntil) {
			state.blockedUntil = blockedUntil
		}
	}
	if failure == nil || !failure.CountsTowardBreaker() {
		if halfOpen && state.state == BreakerHalfOpen {
			state.state = BreakerClosed
			state.failures = nil
			state.openUntil = time.Time{}
			state.probeInFlight = false
		}
		return
	}

	now := b.now()
	if halfOpen && state.state == BreakerHalfOpen {
		b.open(state, now, failure.RetryAfter)
		return
	}
	if state.state != BreakerClosed {
		return
	}
	b.prune(state, now)
	state.failures = append(state.failures, now)
	if len(state.failures) >= b.failureThreshold {
		b.open(state, now, failure.RetryAfter)
	}
}

func (b *CircuitBreaker) open(state *breakerChannelState, now time.Time, retryAfter time.Duration) {
	duration := b.openDuration
	if retryAfter > duration {
		duration = retryAfter
	}
	state.state = BreakerOpen
	state.openUntil = now.Add(duration)
	state.probeInFlight = false
}

type CircuitPermit struct {
	breaker   *CircuitBreaker
	channelID uint
	halfOpen  bool
	once      sync.Once
}

func (p *CircuitPermit) Succeed() {
	if p == nil || p.breaker == nil {
		return
	}
	p.once.Do(func() { p.breaker.succeed(p.channelID, p.halfOpen) })
}

func (p *CircuitPermit) Fail(failure *Failure) {
	if p == nil || p.breaker == nil {
		return
	}
	p.once.Do(func() { p.breaker.fail(p.channelID, p.halfOpen, failure) })
}
