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

const (
	defaultIncreaseInterval = time.Second
	defaultBackoffDebounce  = 500 * time.Millisecond
	minRate                 = 1e-9
)

// Clock supplies the current time for AIMD debouncing and additive windows.
// Use nil in [AIMDRateLimiterConfig] for wall-clock time. In tests, use a fake
// implementation and advance it so behavior is deterministic without sleeps.
type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

var _ Clock = wallClock{}

func clockOrDefault(c Clock) Clock {
	if c == nil {
		return wallClock{}
	}
	return c
}

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
//
// Increase: at most one additive +1 per IncreaseInterval measured from the last applied
// increase (zero means default one second). Use a [Clock] to control this in tests.
//
// Backoff: zero BackoffDebounce selects 500ms coalescing of multiplicative steps.
// Set DisableBackoffMerge so each Backoff() applies a full ×0.5.
type AIMDRateLimiterConfig struct {
	Initial float64
	Min     float64
	Max     float64

	// IncreaseInterval is the minimum time between additive +1 steps (default 1s if zero).
	IncreaseInterval time.Duration

	// BackoffDebounce merges Backoff calls within this duration (default 500ms if zero).
	BackoffDebounce time.Duration
	// DisableBackoffMerge, if true, applies every Backoff() fully (no merge).
	DisableBackoffMerge bool

	// Clock is used for debouncing and increase timing; nil uses wall clock.
	Clock Clock

	// Logger receives Debug logs for rate transitions; nil disables logging.
	Logger *slog.Logger
}

// AIMDRateLimiter implements adaptive rate limiting using AIMD (additive increase, multiplicative decrease).
// The underlying golang.org/x/time/rate limiter is not exposed: use [AIMDRateLimiter.Wait] and the AIMD
// methods so min/max invariants stay consistent.
type AIMDRateLimiter struct {
	lim   *rate.Limiter
	clock Clock
	mu    sync.Mutex

	current float64
	min     float64
	max     float64

	increaseInterval time.Duration

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

	incInterval := cfg.IncreaseInterval
	if incInterval == 0 {
		incInterval = defaultIncreaseInterval
	}

	backoffOff := cfg.DisableBackoffMerge
	backoffDeb := cfg.BackoffDebounce
	if backoffOff {
		backoffDeb = 0
	} else if backoffDeb == 0 {
		backoffDeb = defaultBackoffDebounce
	}

	clk := clockOrDefault(cfg.Clock)

	a := &AIMDRateLimiter{
		lim:                     rate.NewLimiter(rate.Limit(initial), burstForRate(initial)),
		clock:                   clk,
		current:                 initial,
		min:                     minR,
		max:                     maxR,
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

// Backoff applies multiplicative decrease (×0.5, clamped to min), optionally coalesced within BackoffDebounce.
// It resets additive scheduling so recovery after a drop is not delayed by an old window.
func (a *AIMDRateLimiter) Backoff() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.clock.Now()
	if !a.backoffDebounceDisabled && !a.lastBackoff.IsZero() && now.Sub(a.lastBackoff) < a.backoffDebounce {
		return
	}
	a.lastBackoff = now

	a.lastIncrease = time.Time{}

	oldRate := a.current
	a.current *= 0.5
	if a.current < a.min {
		a.current = a.min
	}
	a.applyLimiterSettingsLocked()
	a.logTransition("AIMD backoff", oldRate, a.current)
}

// OnSuccess applies at most one additive +1 per IncreaseInterval (capped at max).
func (a *AIMDRateLimiter) OnSuccess() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.current >= a.max {
		return
	}

	now := a.clock.Now()
	if !a.lastIncrease.IsZero() && now.Sub(a.lastIncrease) < a.increaseInterval {
		return
	}
	a.lastIncrease = now

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
