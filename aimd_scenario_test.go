package jorb

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// simulateHiddenAPIServer models one decision per unit time: if the AIMD client is
// attempting more than the unknown true capacity (trueTPS), the API returns 429 → Backoff;
// otherwise the request succeeds → OnSuccess. This matches using RateLimitError vs success
// when the downstream limit is unknown but observable.
func simulateHiddenAPIServer(clk *fakeClock, lim *AIMDRateLimiter, trueTPS float64, steps int) (series []float64) {
	for i := 0; i < steps; i++ {
		cur := lim.Current()
		series = append(series, cur)
		if cur > trueTPS {
			lim.Backoff()
		} else {
			tickBetweenIncreases(clk)
			lim.OnSuccess()
		}
	}
	return series
}

func statsLastN(series []float64, n int) (min, max, mean float64) {
	if len(series) == 0 {
		return 0, 0, 0
	}
	if n > len(series) {
		n = len(series)
	}
	slice := series[len(series)-n:]
	min = slice[0]
	max = slice[0]
	var sum float64
	for _, v := range slice {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}
	mean = sum / float64(len(slice))
	return min, max, mean
}

// TestAIMD_UnknownCap_HighQuotaUser_RampsUp exercises a user whose true ceiling is 10,000 TPS
// while AIMD starts conservative (low initial). The client should climb toward the ceiling
// (bounded by max) and then oscillate under AIMD sawtooth dynamics.
func TestAIMD_UnknownCap_HighQuotaUser_RampsUp(t *testing.T) {
	const trueTPS = 10_000
	const initial, minR, maxR = 50, 1, 15_000
	const steps = 30_000

	clk := newFakeClock()
	lim := NewAIMDRateLimiterWithConfig(AIMDRateLimiterConfig{
		Initial:             initial,
		Min:                 minR,
		Max:                 maxR,
		Clock:               clk,
		IncreaseInterval:    time.Millisecond,
		DisableBackoffMerge: true,
	})

	series := simulateHiddenAPIServer(clk, lim, trueTPS, steps)
	final := lim.Current()
	lo, hi, mean := statsLastN(series, 2000)

	t.Logf("high-quota user: trueTPS=%v initial=%v max=%v steps=%v", trueTPS, initial, maxR, steps)
	t.Logf("final Current=%.2f (last 2000 steps: min=%.2f max=%.2f mean=%.2f)", final, lo, hi, mean)

	require.Greater(t, final, 5000.0, "should discover that capacity is well above the conservative start")
	assert.LessOrEqual(t, final, float64(maxR)+1.0, "should not exceed configured max")
	assert.Greater(t, mean, 4000.0, "steady-state mean should reflect high ceiling")
}

// TestAIMD_UnknownCap_LowQuotaUser_RampsDown exercises a user whose true ceiling is 10 TPS
// while AIMD starts aggressive (high initial). The client should multiplicatively decrease
// until it can succeed, then sawtooth near the true cap.
func TestAIMD_UnknownCap_LowQuotaUser_RampsDown(t *testing.T) {
	const trueTPS = 10
	const initial, minR, maxR = 8_000, 1, 20_000
	const steps = 25_000

	clk := newFakeClock()
	lim := NewAIMDRateLimiterWithConfig(AIMDRateLimiterConfig{
		Initial:             initial,
		Min:                 minR,
		Max:                 maxR,
		Clock:               clk,
		IncreaseInterval:    time.Millisecond,
		DisableBackoffMerge: true,
	})

	series := simulateHiddenAPIServer(clk, lim, trueTPS, steps)
	final := lim.Current()
	lo, hi, mean := statsLastN(series, 2000)

	t.Logf("low-quota user: trueTPS=%v initial=%v steps=%v", trueTPS, initial, steps)
	t.Logf("final Current=%.4f (last 2000 steps: min=%.4f max=%.4f mean=%.4f)", final, lo, hi, mean)

	assert.LessOrEqual(t, final, 50.0, "should settle in a band near the 10 TPS ceiling, not near initial 8000")
	assert.GreaterOrEqual(t, final, minR-0.01, "should respect floor")
	assert.Less(t, mean, 100.0, "time-average rate should reflect low ceiling after adaptation")
}

// TestAIMD_UnknownCap_ScaledFast is a shorter CI-friendly check with smaller numbers.
func TestAIMD_UnknownCap_ScaledFast(t *testing.T) {
	clk := newFakeClock()
	high := NewAIMDRateLimiterWithConfig(AIMDRateLimiterConfig{
		Initial: 20, Min: 1, Max: 500,
		Clock: clk, IncreaseInterval: time.Millisecond, DisableBackoffMerge: true,
	})
	sHigh := simulateHiddenAPIServer(clk, high, 100, 5000)
	require.Greater(t, high.Current(), 65.0)
	_, _, meanH := statsLastN(sHigh, 500)
	assert.Greater(t, meanH, 65.0)

	clk2 := newFakeClock()
	low := NewAIMDRateLimiterWithConfig(AIMDRateLimiterConfig{
		Initial: 200, Min: 1, Max: 500,
		Clock: clk2, IncreaseInterval: time.Millisecond, DisableBackoffMerge: true,
	})
	sLow := simulateHiddenAPIServer(clk2, low, 10, 5000)
	assert.Less(t, low.Current(), 50.0)
	_, _, meanL := statsLastN(sLow, 500)
	assert.Less(t, meanL, 40.0)

	t.Logf("scaled: high-cap mean(last500)=%.2f low-cap mean(last500)=%.2f", meanH, meanL)
}

// Processor-level AIMD + RateLimitError is covered in processor_test.go (TestProcessor_AIMDBackoff,
// TestProcessor_AIMDWithMultipleJobs). Those use wall-clock [rate.Limiter] pacing; the simulations
// above isolate AIMD adaptation against a hidden TPS ceiling with a fake clock.
