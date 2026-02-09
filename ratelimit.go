package jorb

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/time/rate"
)

// BackoffRateLimiter is an optional interface that rate limiters can implement
// to support dynamic rate adjustment based on downstream feedback
type BackoffRateLimiter interface {
	Backoff()
	OnSuccess()
}

// RateLimitError signals that a rate limit was hit and backoff should occur
type RateLimitError struct {
	Err error
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limit exceeded: %v", e.Err)
}

func (e *RateLimitError) Unwrap() error {
	return e.Err
}

// IsRateLimitError checks if an error is a RateLimitError
func IsRateLimitError(err error) bool {
	var rle *RateLimitError
	return errors.As(err, &rle)
}

// AIMDRateLimiter implements adaptive rate limiting using AIMD (Additive Increase, Multiplicative Decrease)
type AIMDRateLimiter struct {
	*rate.Limiter
	mu      sync.Mutex
	current float64
	min     float64
	max     float64
}

// NewAIMDRateLimiter creates a new AIMD rate limiter with the specified initial, min, and max rates
func NewAIMDRateLimiter(initial, min, max float64) *AIMDRateLimiter {
	return &AIMDRateLimiter{
		Limiter: rate.NewLimiter(rate.Limit(initial), int(initial)),
		current: initial,
		min:     min,
		max:     max,
	}
}

// Backoff implements BackoffRateLimiter - multiplicative decrease
func (a *AIMDRateLimiter) Backoff() {
	a.mu.Lock()
	defer a.mu.Unlock()

	oldRate := a.current
	a.current *= 0.5
	if a.current < a.min {
		a.current = a.min
	}
	a.Limiter.SetLimit(rate.Limit(a.current))
	a.Limiter.SetBurst(int(a.current))
	slog.Info("AIMD backoff", "oldRate", oldRate, "newRate", a.current)
}

// OnSuccess implements BackoffRateLimiter - additive increase
func (a *AIMDRateLimiter) OnSuccess() {
	a.mu.Lock()
	defer a.mu.Unlock()

	oldRate := a.current
	a.current += 1
	if a.current > a.max {
		a.current = a.max
	}
	a.Limiter.SetLimit(rate.Limit(a.current))
	a.Limiter.SetBurst(int(a.current))
	if a.current != oldRate {
		slog.Info("AIMD success", "oldRate", oldRate, "newRate", a.current)
	}
}

// Current returns the current rate limit
func (a *AIMDRateLimiter) Current() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.current
}
