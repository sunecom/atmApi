package glmoptimizer

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestClassifyHTTPFailure(t *testing.T) {
	tests := []struct {
		status       int
		class        FailureClass
		retryable    bool
		counts       bool
		retryAfter   string
		wantCooldown time.Duration
	}{
		{status: 400, class: FailureClientRequest},
		{status: 401, class: FailureChannelAuth, retryable: true},
		{status: 402, class: FailureChannelBalance, retryable: true},
		{status: 403, class: FailureChannelAuth, retryable: true},
		{status: 408, class: FailureChannelTransient, retryable: true, counts: true},
		{status: 422, class: FailureClientRequest},
		{status: 429, class: FailureChannelRateLimit, retryable: true, counts: true, retryAfter: "7", wantCooldown: 7 * time.Second},
		{status: 500, class: FailureChannelTransient, retryable: true, counts: true},
		{status: 503, class: FailureChannelTransient, retryable: true, counts: true},
	}

	for _, test := range tests {
		t.Run(httpStatusName(test.status), func(t *testing.T) {
			failure := NewHTTPFailure(17, test.status, test.retryAfter)
			if failure.Class != test.class || failure.Retryable() != test.retryable || failure.CountsTowardBreaker() != test.counts {
				t.Fatalf("failure = %+v", failure)
			}
			if failure.RetryAfter != test.wantCooldown {
				t.Fatalf("RetryAfter = %v, want %v", failure.RetryAfter, test.wantCooldown)
			}
		})
	}
}

func TestNormalizeFailureCoversNetworkCancellationAndTerminalStates(t *testing.T) {
	networkErr := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")}
	tests := []struct {
		name   string
		err    error
		class  FailureClass
		retry  bool
		counts bool
	}{
		{name: "network", err: networkErr, class: FailureChannelTransient, retry: true, counts: true},
		{name: "cancel", err: context.Canceled, class: FailureClientCancel},
		{name: "deadline", err: context.DeadlineExceeded, class: FailureChannelTransient, retry: true, counts: true},
		{name: "reasoning_only", err: NewTerminalFailure(9, TerminalOutcome{State: TerminalFailureReasoningOnly, ErrorCode: TerminalCodeReasoningOnly}), class: FailureSemanticEmpty, retry: true},
		{name: "empty", err: NewTerminalFailure(9, TerminalOutcome{State: TerminalFailureEmpty, ErrorCode: TerminalCodeEmpty}), class: FailureSemanticEmpty, retry: true},
		{name: "malformed", err: NewTerminalFailure(9, TerminalOutcome{State: TerminalFailureMalformed, ErrorCode: TerminalCodeMalformed}), class: FailureChannelProtocol, retry: true, counts: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := NormalizeFailure(9, test.err)
			if failure.Class != test.class || failure.Retryable() != test.retry || failure.CountsTowardBreaker() != test.counts {
				t.Fatalf("failure = %+v", failure)
			}
		})
	}
}

func TestFailureNeverRetriesAfterClientBytes(t *testing.T) {
	failure := NewHTTPFailure(4, 503, "")
	failure.BytesForwarded = 1
	if failure.Retryable() {
		t.Fatal("failure after the first client byte must not be retryable")
	}
}

func httpStatusName(status int) string {
	return "status_" + string(rune('0'+status/100)) + string(rune('0'+status/10%10)) + string(rune('0'+status%10))
}
