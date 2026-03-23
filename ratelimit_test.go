package jorb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock is a deterministic clock for tests. Advance with Add between steps.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// testAIMD returns a limiter with a fake clock, 1ms increase interval, and no backoff merge.
// Call clk.Add(interval) between OnSuccess steps that should each apply +1.
func testAIMD(t testing.TB, clk *fakeClock, initial, min, max float64) *AIMDRateLimiter {
	t.Helper()
	return NewAIMDRateLimiterWithConfig(AIMDRateLimiterConfig{
		Initial:             initial,
		Min:                 min,
		Max:                 max,
		Clock:               clk,
		IncreaseInterval:    time.Millisecond,
		DisableBackoffMerge: true,
	})
}

func tickBetweenIncreases(clk *fakeClock) {
	clk.Add(time.Millisecond)
}

func TestRateLimitError(t *testing.T) {
	baseErr := errors.New("too many requests")
	rle := &RateLimitError{Err: baseErr}

	if rle.Error() != "rate limit exceeded: too many requests" {
		t.Errorf("Expected error message to contain base error, got: %s", rle.Error())
	}

	if !errors.Is(rle, baseErr) {
		t.Error("Expected RateLimitError to unwrap to base error")
	}

	if !IsRateLimitError(rle) {
		t.Error("Expected IsRateLimitError to return true for RateLimitError")
	}

	if IsRateLimitError(baseErr) {
		t.Error("Expected IsRateLimitError to return false for non-RateLimitError")
	}
}

func TestAIMDRateLimiter_Backoff(t *testing.T) {
	clk := newFakeClock()
	limiter := testAIMD(t, clk, 100, 10, 200)

	if limiter.Current() != 100 {
		t.Errorf("Expected initial rate of 100, got %f", limiter.Current())
	}

	limiter.Backoff()
	if limiter.Current() != 50 {
		t.Errorf("Expected rate of 50 after backoff, got %f", limiter.Current())
	}

	limiter.Backoff()
	if limiter.Current() != 25 {
		t.Errorf("Expected rate of 25 after second backoff, got %f", limiter.Current())
	}

	limiter.Backoff()
	if limiter.Current() != 12.5 {
		t.Errorf("Expected rate of 12.5 after third backoff, got %f", limiter.Current())
	}

	limiter.Backoff()
	if limiter.Current() != 10 {
		t.Errorf("Expected rate to be clamped at min of 10, got %f", limiter.Current())
	}
}

func TestAIMDRateLimiter_OnSuccess(t *testing.T) {
	clk := newFakeClock()
	limiter := testAIMD(t, clk, 100, 10, 200)

	for i := 0; i < 100; i++ {
		tickBetweenIncreases(clk)
		limiter.OnSuccess()
	}

	if limiter.Current() != 200 {
		t.Errorf("Expected rate to be clamped at max of 200, got %f", limiter.Current())
	}

	limiter.OnSuccess()
	if limiter.Current() != 200 {
		t.Errorf("Expected rate to stay at max of 200, got %f", limiter.Current())
	}
}

func TestAIMDRateLimiter_SawtoothPattern(t *testing.T) {
	clk := newFakeClock()
	limiter := testAIMD(t, clk, 100, 10, 200)

	for i := 0; i < 50; i++ {
		tickBetweenIncreases(clk)
		limiter.OnSuccess()
	}
	if limiter.Current() != 150 {
		t.Errorf("Expected rate of 150 after 50 successes, got %f", limiter.Current())
	}

	limiter.Backoff()
	if limiter.Current() != 75 {
		t.Errorf("Expected rate of 75 after backoff, got %f", limiter.Current())
	}

	for i := 0; i < 25; i++ {
		tickBetweenIncreases(clk)
		limiter.OnSuccess()
	}
	if limiter.Current() != 100 {
		t.Errorf("Expected rate of 100 after 25 more successes, got %f", limiter.Current())
	}
}

func TestAIMDRateLimiter_ImplementsBackoffRateLimiter(t *testing.T) {
	var _ BackoffRateLimiter = &AIMDRateLimiter{}
}

func TestAIMDRateLimiter_Integration(t *testing.T) {
	clk := newFakeClock()
	limiter := testAIMD(t, clk, 100, 10, 200)

	for i := 0; i < 10; i++ {
		tickBetweenIncreases(clk)
		limiter.OnSuccess()
	}
	if limiter.Current() != 110 {
		t.Errorf("Expected rate of 110 after 10 successes, got %f", limiter.Current())
	}

	limiter.Backoff()
	if limiter.Current() != 55 {
		t.Errorf("Expected rate of 55 after backoff, got %f", limiter.Current())
	}

	for i := 0; i < 20; i++ {
		tickBetweenIncreases(clk)
		limiter.OnSuccess()
	}
	if limiter.Current() != 75 {
		t.Errorf("Expected rate of 75 after recovery, got %f", limiter.Current())
	}

	for i := 0; i < 10; i++ {
		limiter.Backoff()
	}
	if limiter.Current() != 10 {
		t.Errorf("Expected rate to be at minimum of 10, got %f", limiter.Current())
	}
}

func TestAIMDRateLimiter_Concurrency(t *testing.T) {
	clk := newFakeClock()
	limiter := testAIMD(t, clk, 100, 10, 200)
	done := make(chan bool)

	for i := 0; i < 10; i++ {
		go func() {
			limiter.Backoff()
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		go func() {
			limiter.OnSuccess()
			done <- true
		}()
	}

	for i := 0; i < 20; i++ {
		<-done
	}

	// Advance clock so a single OnSuccess can apply if below max
	clk.Add(time.Millisecond)

	current := limiter.Current()
	if current < 10 || current > 200 {
		t.Errorf("Expected rate to be within bounds [10, 200], got %f", current)
	}
}

func TestAIMDRateLimiter_DefaultIncreaseDebouncing(t *testing.T) {
	clk := newFakeClock()
	limiter := NewAIMDRateLimiterWithConfig(AIMDRateLimiterConfig{
		Initial:             100,
		Min:                 10,
		Max:                 200,
		Clock:               clk,
		IncreaseInterval:    500 * time.Millisecond,
		DisableBackoffMerge: true,
	})

	for i := 0; i < 50; i++ {
		limiter.OnSuccess()
	}
	if got := limiter.Current(); got != 101 {
		t.Errorf("Expected +1 over 500ms window, got %f", got)
	}

	clk.Add(600 * time.Millisecond)
	limiter.OnSuccess()
	if got := limiter.Current(); got != 102 {
		t.Errorf("Expected second increase after interval, got %f", got)
	}
}

func TestAIMDRateLimiter_DefaultBackoffCoalescing(t *testing.T) {
	clk := newFakeClock()
	limiter := NewAIMDRateLimiterWithConfig(AIMDRateLimiterConfig{
		Initial: 100,
		Min:     10,
		Max:     200,
		Clock:   clk,
	})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			limiter.Backoff()
		}()
	}
	wg.Wait()
	if got := limiter.Current(); got != 50 {
		t.Errorf("Expected single effective backoff in debounce window (~50), got %f", got)
	}
}

func TestAIMDRateLimiter_AtMaxDoesNotBlockRecovery(t *testing.T) {
	clk := newFakeClock()
	limiter := NewAIMDRateLimiterWithConfig(AIMDRateLimiterConfig{
		Initial:             200,
		Min:                 10,
		Max:                 200,
		Clock:               clk,
		IncreaseInterval:    time.Hour,
		DisableBackoffMerge: true,
	})
	limiter.Backoff()
	if got := limiter.Current(); got != 100 {
		t.Fatalf("after backoff want 100, got %f", got)
	}
	limiter.OnSuccess()
	if got := limiter.Current(); got != 101 {
		t.Fatalf("recovery increase blocked: want 101, got %f", got)
	}
}

func TestAIMDRateLimiter_BoundaryConditions(t *testing.T) {
	tests := []struct {
		name    string
		initial float64
		min     float64
		max     float64
	}{
		{"zero initial", 0, 0, 100},
		{"same min and max", 50, 50, 50},
		{"initial equals min", 10, 10, 100},
		{"initial equals max", 100, 10, 100},
		{"very small values", 0.1, 0.01, 1},
		{"very large values", 10000, 1000, 50000},
		{"min greater than max swapped", 30, 100, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := NewAIMDRateLimiter(tt.initial, tt.min, tt.max)

			limiter.Backoff()
			limiter.OnSuccess()

			current := limiter.Current()
			lo := limiter.Min()
			hi := limiter.Max()
			if current < lo || current > hi {
				t.Errorf("Rate %f outside bounds [%f, %f]", current, lo, hi)
			}
		})
	}
}

func TestAIMDRateLimiter_Wait(t *testing.T) {
	limiter := NewAIMDRateLimiter(100, 10, 200)

	ctx := context.Background()
	if err := limiter.Wait(ctx); err != nil {
		t.Errorf("Expected Wait to succeed, got error: %v", err)
	}
}

func TestRateLimitError_Wrapping(t *testing.T) {
	baseErr := errors.New("HTTP 429: Too Many Requests")
	rle := &RateLimitError{Err: baseErr}

	if !strings.Contains(rle.Error(), "rate limit exceeded") {
		t.Errorf("Error message should contain 'rate limit exceeded'")
	}
	if !strings.Contains(rle.Error(), baseErr.Error()) {
		t.Errorf("Error message should contain base error")
	}

	if !errors.Is(rle, baseErr) {
		t.Error("RateLimitError should unwrap to base error")
	}

	rleNil := &RateLimitError{Err: nil}
	if !strings.Contains(rleNil.Error(), "<nil>") {
		t.Errorf("Should handle nil error gracefully")
	}
}

func TestIsRateLimitError_NestedErrors(t *testing.T) {
	baseErr := errors.New("connection failed")
	rle := &RateLimitError{Err: baseErr}
	wrappedErr := fmt.Errorf("wrapped: %w", rle)
	doubleWrappedErr := fmt.Errorf("double wrapped: %w", wrappedErr)

	if !IsRateLimitError(rle) {
		t.Error("Should detect direct RateLimitError")
	}
	if !IsRateLimitError(wrappedErr) {
		t.Error("Should detect wrapped RateLimitError")
	}
	if !IsRateLimitError(doubleWrappedErr) {
		t.Error("Should detect double-wrapped RateLimitError")
	}
	if IsRateLimitError(baseErr) {
		t.Error("Should not detect non-RateLimitError")
	}
	if IsRateLimitError(nil) {
		t.Error("Should handle nil error")
	}
}
