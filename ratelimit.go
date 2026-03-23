package jorb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

var _ RateLimiter = (*AIMDRateLimiter)(nil)

// Sentinel durations for [AIMDRateLimiterConfig].
const (
	// AIMDIncreaseEverySuccess disables time-based additive debouncing: each OnSuccess
	// applies +1 (capped at max). Useful mainly for tests or very aggressive ramp-up.
	AIMDIncreaseEverySuccess time.Duration = -1
	// AIMDDebounceDisabled applies every Backoff() fully (no merging of backoffs in time).
	AIMDDebounceDisabled time.Duration = -1
)

const (
	defaultIncreaseInterval = time.Second
	defaultBackoffDebounce  = 500 * time.Millisecond
	minRate                 = 1e-9
)

// BackoffRateLimiter is an optional interface that rate limiters can implement
// to support dynamic rate adjustment based on downstream feedback.
type BackoffRateLimiter interface {
	Backoff()
	OnSuccess()
}

// RateLimitError signals that a rate limit was hit and backoff should occur.
type RateLimitError struct {
	Err error
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limit exceeded: %v", e.Err)
}

func (e *RateLimitError) Unwrap() error {
	return e.Err
}

// IsRateLimitError checks if an error is a RateLimitError (including wrapped).
func IsRateLimitError(err error) bool {
	var rle *RateLimitError
	return errors.As(err, &rle)
}

// AIMDRateLimiterConfig configures [NewAIMDRateLimiterWithConfig].
// Zero IncreaseInterval selects a 1s additive window; zero BackoffDebounce selects 500ms
// coalescing of multiplicative backoffs (reduces ×0.5 stacking when many workers hit limits at once).
// Use [AIMDIncreaseEverySuccess] / [AIMDDebounceDisabled] to opt out.
type AIMDRateLimiterConfig struct {
	Initial float64
	Min     float64
	Max     float64
	// IncreaseInterval is the minimum time between additive +1 steps. Each OnSuccess may run
	// more often, but the limit only rises at most once per interval (unless AIMDIncreaseEverySuccess).
	IncreaseInterval time.Duration
	// BackoffDebounce merges multiple Backoff calls within this window into at most one ×0.5 step.
	BackoffDebounce time.Duration
	// Logger receives Debug logs for rate transitions; nil disables logging.
	Logger *slog.Logger
}

// AIMDRateLimiter implements adaptive rate limiting using AIMD (additive increase, multiplicative decrease).
// The underlying golang.org/x/time/rate limiter is not exposed: use [AIMDRateLimiter.Wait] and the AIMD
// methods so min/max invariants stay consistent.
type AIMDRateLimiter struct {
	lim *rate.Limiter
	mu  sync.Mutex

	current float64
	min     float64
	max     float64

	increaseEverySuccess bool
	increaseInterval     time.Duration

	backoffDebounceDisabled bool
	backoffDebounce         time.Duration

	lastBackoff  time.Time
	lastIncrease time.Time

	log *slog.Logger
}

// NewAIMDRateLimiter creates an AIMD limiter with defaults: 1s between additive steps and 500ms
// backoff coalescing. Initial, min, and max are normalized (see [normalizeAIMDRates]).
func NewAIMDRateLimiter(initial, min, max float64) *AIMDRateLimiter {
	return NewAIMDRateLimiterWithConfig(AIMDRateLimiterConfig{
		Initial: initial,
		Min:     min,
		Max:     max,
	})
}

// NewAIMDRateLimiterWithConfig is like [NewAIMDRateLimiter] with explicit tuning.
func NewAIMDRateLimiterWithConfig(cfg AIMDRateLimiterConfig) *AIMDRateLimiter {
	initial, minR, maxR := normalizeAIMDRates(cfg.Initial, cfg.Min, cfg.Max)

	incEvery := cfg.IncreaseInterval == AIMDIncreaseEverySuccess
	incInterval := cfg.IncreaseInterval
	if incEvery {
		incInterval = 0
	} else if incInterval == 0 {
		incInterval = defaultIncreaseInterval
	}

	backoffOff := cfg.BackoffDebounce == AIMDDebounceDisabled
	backoffDeb := cfg.BackoffDebounce
	if backoffOff {
		backoffDeb = 0
	} else if backoffDeb == 0 {
		backoffDeb = defaultBackoffDebounce
	}

	a := &AIMDRateLimiter{
		lim:                     rate.NewLimiter(rate.Limit(initial), burstForRate(initial)),
		current:                 initial,
		min:                     minR,
		max:                     maxR,
		increaseEverySuccess:    incEvery,
		increaseInterval:        incInterval,
		backoffDebounceDisabled: backoffOff,
		backoffDebounce:         backoffDeb,
		log:                     cfg.Logger,
	}
	a.applyLimiterSettingsLocked()
	return a
}

// Wait blocks until the limiter permits an event or ctx is cancelled.
func (a *AIMDRateLimiter) Wait(ctx context.Context) error {
	return a.lim.Wait(ctx)
}

// Backoff applies multiplicative decrease (×0.5, clamped to min), coalesced within BackoffDebounce
// unless [AIMDDebounceDisabled] was set.
func (a *AIMDRateLimiter) Backoff() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	if !a.backoffDebounceDisabled && !a.lastBackoff.IsZero() && now.Sub(a.lastBackoff) < a.backoffDebounce {
		return
	}
	a.lastBackoff = now

	oldRate := a.current
	a.current *= 0.5
	if a.current < a.min {
		a.current = a.min
	}
	a.applyLimiterSettingsLocked()
	a.logTransition("AIMD backoff", oldRate, a.current)
}

// OnSuccess applies additive increase (+1 req/s per increase window, capped at max), or on every
// success when configured with [AIMDIncreaseEverySuccess].
func (a *AIMDRateLimiter) OnSuccess() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	if !a.increaseEverySuccess {
		if !a.lastIncrease.IsZero() && now.Sub(a.lastIncrease) < a.increaseInterval {
			return
		}
		a.lastIncrease = now
	}

	oldRate := a.current
	a.current++
	if a.current > a.max {
		a.current = a.max
	}
	if a.current == oldRate {
		return
	}
	a.applyLimiterSettingsLocked()
	a.logTransition("AIMD success", oldRate, a.current)
}

// Current returns the configured rate in events per second.
func (a *AIMDRateLimiter) Current() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.current
}

// Min returns the configured minimum rate.
func (a *AIMDRateLimiter) Min() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.min
}

// Max returns the configured maximum rate.
func (a *AIMDRateLimiter) Max() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.max
}

func (a *AIMDRateLimiter) applyLimiterSettingsLocked() {
	a.lim.SetLimit(rate.Limit(a.current))
	// Burst tracks the limit (≈ one second of burst); fractional rates use Ceil so the bucket
	// is not systematically undersized by truncation.
	a.lim.SetBurst(burstForRate(a.current))
}

func (a *AIMDRateLimiter) logTransition(msg string, oldR, newR float64) {
	if a.log == nil {
		return
	}
	a.log.Debug(msg, "oldRate", oldR, "newRate", newR)
}

func normalizeAIMDRates(initial, minV, maxV float64) (float64, float64, float64) {
	if minV > maxV {
		minV, maxV = maxV, minV
	}
	if minV < minRate {
		minV = minRate
	}
	if maxV < minV {
		maxV = minV
	}
	if initial <= 0 || initial < minV {
		initial = minV
	}
	if initial > maxV {
		initial = maxV
	}
	return initial, minV, maxV
}

func burstForRate(r float64) int {
	if r <= 0 {
		return 1
	}
	b := int(math.Ceil(r))
	if b < 1 {
		return 1
	}
	return b
}
