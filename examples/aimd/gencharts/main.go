// Command gencharts writes PNG time-series charts for AIMD hidden-capacity scenarios
// (same model as aimd_scenario_test.go). Run from repo: go run ./examples/aimd/gencharts
package main

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"time"

	"github.com/gaffo/jorb"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
)

type fakeClock struct {
	t time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) Now() time.Time { return c.t }

func (c *fakeClock) Add(d time.Duration) { c.t = c.t.Add(d) }

func simulate(clk *fakeClock, lim *jorb.AIMDRateLimiter, trueTPS float64, steps int) []float64 {
	series := make([]float64, 0, steps)
	for i := 0; i < steps; i++ {
		cur := lim.Current()
		series = append(series, cur)
		if cur > trueTPS {
			lim.Backoff()
		} else {
			clk.Add(time.Millisecond)
			lim.OnSuccess()
		}
	}
	return series
}

func downsample(xs, ys []float64, maxPoints int) ([]float64, []float64) {
	if len(xs) <= maxPoints {
		return xs, ys
	}
	step := len(xs) / maxPoints
	if step < 1 {
		step = 1
	}
	var ox, oy []float64
	for i := 0; i < len(xs); i += step {
		ox = append(ox, xs[i])
		oy = append(oy, ys[i])
	}
	return ox, oy
}

func writeLineChart(path, title, yLabel string, series []float64, trueTPS float64) error {
	p := plot.New()
	p.Title.Text = title
	p.X.Label.Text = "simulation step"
	p.Y.Label.Text = yLabel
	p.Add(plotter.NewGrid())

	n := len(series)
	xs := make([]float64, n)
	for i := range xs {
		xs[i] = float64(i)
	}
	xs, ys := downsample(xs, series, 8000)

	pts := make(plotter.XYs, len(xs))
	for i := range xs {
		pts[i].X = xs[i]
		pts[i].Y = ys[i]
	}
	line, err := plotter.NewLine(pts)
	if err != nil {
		return err
	}
	line.Color = color.NRGBA{R: 0x22, G: 0x66, B: 0xCC, A: 0xff}
	line.Width = vg.Points(0.8)
	p.Add(line)

	capPts := plotter.XYs{{X: xs[0], Y: trueTPS}, {X: xs[len(xs)-1], Y: trueTPS}}
	capLine, err := plotter.NewLine(capPts)
	if err != nil {
		return err
	}
	capLine.Color = color.NRGBA{R: 0xCC, G: 0x44, B: 0x44, A: 0xff}
	capLine.Dashes = []vg.Length{vg.Points(4), vg.Points(3)}
	capLine.Width = vg.Points(0.6)
	p.Add(capLine)
	p.Legend.Add("AIMD Current()", line)
	p.Legend.Add("true API ceiling (TPS)", capLine)
	p.Legend.Top = true

	w := 10 * vg.Inch
	h := 4 * vg.Inch
	if err := p.Save(w, h, path); err != nil {
		return err
	}
	return nil
}

func defaultChartDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return filepath.Join("examples", "aimd", "charts")
	}
	if filepath.Base(wd) == "gencharts" {
		return filepath.Join(wd, "..", "charts")
	}
	return filepath.Join(wd, "examples", "aimd", "charts")
}

func main() {
	outDir := defaultChartDir()
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}

	// Scenario A: high quota (10k), conservative start
	const highTrue, highInit, highMin, highMax = 10_000.0, 50.0, 1.0, 15_000.0
	const highSteps = 30_000
	clk1 := newFakeClock()
	lim1 := jorb.NewAIMDRateLimiterWithConfig(jorb.AIMDRateLimiterConfig{
		Initial: highInit, Min: highMin, Max: highMax,
		Clock: clk1, IncreaseInterval: time.Millisecond, DisableBackoffMerge: true,
	})
	s1 := simulate(clk1, lim1, highTrue, highSteps)
	path1 := filepath.Join(outDir, "scenario_high_quota_10k.png")
	if err := writeLineChart(path1,
		"High-quota user (true ceiling 10,000 TPS)",
		"AIMD rate (req/s)",
		s1, highTrue); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	fmt.Println(path1)

	// Scenario B: low quota (10), aggressive start
	const lowTrue, lowInit, lowMin, lowMax = 10.0, 8000.0, 1.0, 20_000.0
	const lowSteps = 25_000
	clk2 := newFakeClock()
	lim2 := jorb.NewAIMDRateLimiterWithConfig(jorb.AIMDRateLimiterConfig{
		Initial: lowInit, Min: lowMin, Max: lowMax,
		Clock: clk2, IncreaseInterval: time.Millisecond, DisableBackoffMerge: true,
	})
	s2 := simulate(clk2, lim2, lowTrue, lowSteps)
	path2 := filepath.Join(outDir, "scenario_low_quota_10.png")
	if err := writeLineChart(path2,
		"Low-quota user (true ceiling 10 TPS)",
		"AIMD rate (req/s)",
		s2, lowTrue); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	fmt.Println(path2)

	// Scenario C: scaled (CI-friendly) — two charts
	clk3 := newFakeClock()
	h3 := jorb.NewAIMDRateLimiterWithConfig(jorb.AIMDRateLimiterConfig{
		Initial: 20, Min: 1, Max: 500,
		Clock: clk3, IncreaseInterval: time.Millisecond, DisableBackoffMerge: true,
	})
	s3a := simulate(clk3, h3, 100, 5000)
	path3a := filepath.Join(outDir, "scenario_scaled_high_100.png")
	if err := writeLineChart(path3a,
		"Scaled: discover high cap (true 100 TPS)",
		"AIMD rate (req/s)",
		s3a, 100); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	fmt.Println(path3a)

	clk4 := newFakeClock()
	h4 := jorb.NewAIMDRateLimiterWithConfig(jorb.AIMDRateLimiterConfig{
		Initial: 200, Min: 1, Max: 500,
		Clock: clk4, IncreaseInterval: time.Millisecond, DisableBackoffMerge: true,
	})
	s3b := simulate(clk4, h4, 10, 5000)
	path3b := filepath.Join(outDir, "scenario_scaled_low_10.png")
	if err := writeLineChart(path3b,
		"Scaled: discover low cap (true 10 TPS)",
		"AIMD rate (req/s)",
		s3b, 10); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	fmt.Println(path3b)
}
