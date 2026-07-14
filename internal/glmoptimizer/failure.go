package glmoptimizer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type FailureClass string

const (
	FailureClientRequest    FailureClass = "client_request"
	FailureChannelAuth      FailureClass = "channel_auth"
	FailureChannelBalance   FailureClass = "channel_balance"
	FailureChannelRateLimit FailureClass = "channel_rate_limit"
	FailureChannelTransient FailureClass = "channel_transient"
	FailureChannelProtocol  FailureClass = "channel_protocol"
	FailureChannelCapacity  FailureClass = "channel_capacity"
	FailureSemanticEmpty    FailureClass = "semantic_empty"
	FailureClientCancel     FailureClass = "client_cancel"
)

// Failure is a safe, typed routing failure. It deliberately excludes upstream
// response bodies, prompts, authorization headers, and channel credentials.
type Failure struct {
	Class          FailureClass
	ChannelID      uint
	StatusCode     int
	RetryAfter     time.Duration
	BytesForwarded int64
	TerminalState  TerminalState
	TerminalCode   string
	Cause          error
}

func (f *Failure) Error() string {
	if f == nil {
		return "GLM-5.2 routing failure"
	}
	parts := []string{fmt.Sprintf("GLM-5.2 %s failure", f.Class)}
	if f.ChannelID != 0 {
		parts = append(parts, fmt.Sprintf("channel_id=%d", f.ChannelID))
	}
	if f.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("status=%d", f.StatusCode))
	}
	if f.TerminalCode != "" {
		parts = append(parts, fmt.Sprintf("code=%s", f.TerminalCode))
	}
	return strings.Join(parts, " ")
}

func (f *Failure) Unwrap() error {
	if f == nil {
		return nil
	}
	return f.Cause
}

func (f *Failure) Retryable() bool {
	if f == nil || f.BytesForwarded > 0 {
		return false
	}
	switch f.Class {
	case FailureChannelAuth, FailureChannelBalance, FailureChannelRateLimit,
		FailureChannelTransient, FailureChannelProtocol, FailureChannelCapacity, FailureSemanticEmpty:
		return true
	default:
		return false
	}
}

func (f *Failure) CountsTowardBreaker() bool {
	if f == nil {
		return false
	}
	switch f.Class {
	case FailureChannelRateLimit, FailureChannelTransient, FailureChannelProtocol:
		return true
	default:
		return false
	}
}

func NewHTTPFailure(channelID uint, statusCode int, retryAfter string) *Failure {
	return &Failure{
		Class:      classifyHTTPStatus(statusCode),
		ChannelID:  channelID,
		StatusCode: statusCode,
		RetryAfter: parseRetryAfter(retryAfter, time.Now()),
	}
}

func NewTerminalFailure(channelID uint, outcome TerminalOutcome) *Failure {
	class := FailureSemanticEmpty
	if outcome.State == TerminalFailureMalformed {
		class = FailureChannelProtocol
	}
	return &Failure{
		Class:         class,
		ChannelID:     channelID,
		TerminalState: outcome.State,
		TerminalCode:  outcome.ErrorCode,
	}
}

func NormalizeFailure(channelID uint, err error) *Failure {
	if err == nil {
		return nil
	}
	var typed *Failure
	if errors.As(err, &typed) {
		copy := *typed
		if copy.ChannelID == 0 {
			copy.ChannelID = channelID
		}
		return &copy
	}
	if errors.Is(err, context.Canceled) {
		return &Failure{Class: FailureClientCancel, ChannelID: channelID, Cause: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Failure{Class: FailureChannelTransient, ChannelID: channelID, Cause: err}
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return &Failure{Class: FailureChannelTransient, ChannelID: channelID, Cause: err}
	}
	return &Failure{Class: FailureChannelTransient, ChannelID: channelID, Cause: err}
}

func HTTPStatusForFailure(err error) int {
	failure := NormalizeFailure(0, err)
	if failure == nil {
		return http.StatusInternalServerError
	}
	switch failure.Class {
	case FailureClientRequest:
		if failure.StatusCode == http.StatusUnprocessableEntity {
			return http.StatusUnprocessableEntity
		}
		return http.StatusBadRequest
	case FailureClientCancel:
		return 499
	case FailureChannelRateLimit:
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

func classifyHTTPStatus(statusCode int) FailureClass {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return FailureChannelAuth
	case http.StatusPaymentRequired:
		return FailureChannelBalance
	case http.StatusTooManyRequests:
		return FailureChannelRateLimit
	case http.StatusRequestTimeout:
		return FailureChannelTransient
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return FailureClientRequest
	}
	if statusCode >= 500 {
		return FailureChannelTransient
	}
	if statusCode >= 400 && statusCode < 500 {
		return FailureClientRequest
	}
	return FailureChannelProtocol
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil && retryAt.After(now) {
		return retryAt.Sub(now)
	}
	return 0
}
