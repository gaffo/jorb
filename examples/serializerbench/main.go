// Command serializerbench compares JsonSerializer compact vs pretty-printed JSON across
// job counts and per-job payload sizes. Run from repo: go run ./examples/serializerbench -out ./out
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gaffo/jorb"
	"github.com/schollz/progressbar/v3"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"
)

const iterations = 30

type benchCell struct {
	records                int
	payload                int
	prettyAvgNs            int64
	compactAvgNs           int64
	ratioPrettyOverCompact float64 // >1 means compact is faster
}

func main() {
	outDir := flag.String("out", "serializerbench_out", "output directory for CSV and PNGs")
	fullGrid := flag.Bool("full", false, "include extreme n×payload cells (very slow; JSON output can be huge)")
	maxProduct := flag.Int64("max-product-bytes", 200<<20, "skip cells when nRecords×payloadBytes exceeds this (unless -full); crude proxy for output size")
	itersFlag := flag.Int("iterations", iterations, "runs per cell per mode (pretty and compact)")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0750); err != nil {
		log.Fatal(err)
	}

	iters := *itersFlag
	if iters < 1 {
		iters = 1
	}

	// 10 .. 100_000 (log-ish steps)
	recordCounts := []int{10, 100, 1000, 10000, 100000}
	// 100 KiB .. 1 MiB per job String field
	payloadSizes := []int{
		100 << 10,
		200 << 10,
		400 << 10,
		600 << 10,
		800 << 10,
		1 << 20,
	}

	type pair struct{ n, p int }
	var pairs []pair
	var skipped int
	for _, nRec := range recordCounts {
		for _, pay := range payloadSizes {
			prod := int64(nRec) * int64(pay)
			if !*fullGrid && prod > *maxProduct {
				skipped++
				continue
			}
			pairs = append(pairs, pair{nRec, pay})
		}
	}
	if skipped > 0 {
		log.Printf("skipped %d n×payload cells over -max-product-bytes=%d (use -full for entire grid)\n", skipped, *maxProduct)
	}
	if len(pairs) == 0 {
		log.Fatal("no cells to run; raise -max-product-bytes or use -full")
	}

	nCells := len(pairs)
	totalRuns := nCells * iters * 2 // pretty + compact per cell

	bar := progressbar.NewOptions(totalRuns,
		progressbar.OptionSetDescription("serialize"),
		progressbar.OptionShowCount(),
		progressbar.OptionSetWidth(40),
		progressbar.OptionClearOnFinish(),
		progressbar.OptionSetItsString("runs"),
	)

	results := make([]benchCell, 0, nCells)

	tmpRoot, err := os.MkdirTemp("", "serializerbench-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(tmpRoot)

	for cellIdx, q := range pairs {
		nRec, pay := q.n, q.p
		dir := filepath.Join(tmpRoot, fmt.Sprintf("cell_%d", cellIdx))
		if err := os.MkdirAll(dir, 0750); err != nil {
			log.Fatal(err)
		}

		run := buildRun(nRec, pay)

		prettyAvg := benchSerialize(&run, true, dir, "p", iters, bar)
		compactAvg := benchSerialize(&run, false, dir, "c", iters, bar)

		ratio := float64(prettyAvg) / float64(compactAvg)
		if compactAvg == 0 {
			ratio = math.NaN()
		}

		results = append(results, benchCell{
			records:                nRec,
			payload:                pay,
			prettyAvgNs:            prettyAvg.Nanoseconds(),
			compactAvgNs:           compactAvg.Nanoseconds(),
			ratioPrettyOverCompact: ratio,
		})
	}

	_ = bar.Finish()

	csvPath := filepath.Join(*outDir, "results.csv")
	if err := writeCSV(csvPath, results); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Wrote %s\n", csvPath)

	// Slice charts: prefer median axes when those cells exist
	fixedPayload := payloadSizes[len(payloadSizes)/2]
	fixedRecords := recordCounts[len(recordCounts)/2]
	if findCell(results, recordCounts[0], fixedPayload) == nil && len(results) > 0 {
		fixedPayload = results[0].payload
	}
	if findCell(results, fixedRecords, payloadSizes[0]) == nil && len(results) > 0 {
		fixedRecords = results[0].records
	}

	if err := writeLineByRecords(*outDir, results, recordCounts, fixedPayload); err != nil {
		log.Printf("chart_records: %v", err)
	}
	if err := writeLineByPayload(*outDir, results, payloadSizes, fixedRecords); err != nil {
		log.Printf("chart_payload: %v", err)
	}
	if err := writeHeatmap(*outDir, results, recordCounts, payloadSizes); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Charts in %s\n", *outDir)
}

func buildRun(nRecords, payloadLen int) jorb.Run[jorb.MyOverallContext, jorb.MyJobContext] {
	padding := strings.Repeat("x", payloadLen)
	r := jorb.NewRun[jorb.MyOverallContext, jorb.MyJobContext]("bench", jorb.MyOverallContext{Name: "overall"})
	for i := 0; i < nRecords; i++ {
		r.AddJob(jorb.MyJobContext{
			Count:  i,
			Name:   fmt.Sprintf("job-%d", i),
			String: padding,
		})
	}
	return *r
}

func benchSerialize(run *jorb.Run[jorb.MyOverallContext, jorb.MyJobContext], pretty bool, dir, prefix string, iters int, bar *progressbar.ProgressBar) time.Duration {
	var sum time.Duration
	for i := 0; i < iters; i++ {
		path := filepath.Join(dir, fmt.Sprintf("%s_%04d.json", prefix, i))
		ser := &jorb.JsonSerializer[jorb.MyOverallContext, jorb.MyJobContext]{
			File:   path,
			Pretty: pretty,
		}
		start := time.Now()
		if err := ser.Serialize(*run); err != nil {
			log.Fatal(err)
		}
		sum += time.Since(start)
		if bar != nil {
			_ = bar.Add(1)
		}
	}
	return sum / time.Duration(iters)
}

func writeCSV(path string, rows []benchCell) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = fmt.Fprintf(f, "records,payload_bytes,pretty_avg_ns,compact_avg_ns,ratio_pretty_over_compact\n")
	if err != nil {
		return err
	}
	for _, r := range rows {
		_, err = fmt.Fprintf(f, "%d,%d,%d,%d,%g\n", r.records, r.payload, r.prettyAvgNs, r.compactAvgNs, r.ratioPrettyOverCompact)
		if err != nil {
			return err
		}
	}
	return nil
}

func writeLineByRecords(outDir string, rows []benchCell, recordCounts []int, fixedPayload int) error {
	var prettyPts, compactPts plotter.XYs
	for _, n := range recordCounts {
		c := findCell(rows, n, fixedPayload)
		if c == nil {
			continue
		}
		lx := math.Log10(float64(n))
		prettyPts = append(prettyPts, plotter.XY{X: lx, Y: float64(c.prettyAvgNs) / 1e6})
		compactPts = append(compactPts, plotter.XY{X: lx, Y: float64(c.compactAvgNs) / 1e6})
	}
	if len(prettyPts) == 0 {
		return fmt.Errorf("no data for fixed payload=%d", fixedPayload)
	}

	p := plot.New()
	p.Title.Text = fmt.Sprintf("Serialize time vs job count (payload=%d bytes)", fixedPayload)
	p.X.Label.Text = "log10(records)"
	p.Y.Label.Text = "avg wall time (ms)"
	p.Add(plotter.NewGrid())

	lp, err := plotter.NewLine(prettyPts)
	if err != nil {
		return err
	}
	lp.Color = plotutil.Color(0)
	lp.Width = vg.Points(1.5)

	lc, err := plotter.NewLine(compactPts)
	if err != nil {
		return err
	}
	lc.Color = plotutil.Color(1)
	lc.Width = vg.Points(1.5)

	p.Add(lp, lc)
	p.Legend.Top = true
	p.Legend.Left = true
	p.Legend.Add("pretty", lp)
	p.Legend.Add("compact", lc)

	path := filepath.Join(outDir, "chart_records_fixed_payload.png")
	if err := p.Save(10*vg.Inch, 6*vg.Inch, path); err != nil {
		return err
	}
	fmt.Printf("Wrote %s\n", path)
	return nil
}

func writeLineByPayload(outDir string, rows []benchCell, payloadSizes []int, fixedRecords int) error {
	var prettyPts, compactPts plotter.XYs
	for _, pay := range payloadSizes {
		c := findCell(rows, fixedRecords, pay)
		if c == nil {
			continue
		}
		x := float64(pay) / (1024 * 1024) // MiB per job
		prettyPts = append(prettyPts, plotter.XY{X: x, Y: float64(c.prettyAvgNs) / 1e6})
		compactPts = append(compactPts, plotter.XY{X: x, Y: float64(c.compactAvgNs) / 1e6})
	}
	if len(prettyPts) == 0 {
		return fmt.Errorf("no data for fixed records=%d", fixedRecords)
	}

	p := plot.New()
	p.Title.Text = fmt.Sprintf("Serialize time vs per-job payload (%d jobs)", fixedRecords)
	p.X.Label.Text = "payload (MiB per job)"
	p.Y.Label.Text = "avg wall time (ms)"
	p.Add(plotter.NewGrid())

	lp, err := plotter.NewLine(prettyPts)
	if err != nil {
		return err
	}
	lp.Color = plotutil.Color(0)
	lp.Width = vg.Points(1.5)

	lc, err := plotter.NewLine(compactPts)
	if err != nil {
		return err
	}
	lc.Color = plotutil.Color(1)
	lc.Width = vg.Points(1.5)

	p.Add(lp, lc)
	p.Legend.Top = true
	p.Legend.Left = true
	p.Legend.Add("pretty", lp)
	p.Legend.Add("compact", lc)

	path := filepath.Join(outDir, "chart_payload_fixed_records.png")
	if err := p.Save(10*vg.Inch, 6*vg.Inch, path); err != nil {
		return err
	}
	fmt.Printf("Wrote %s\n", path)
	return nil
}

func findCell(rows []benchCell, records, payload int) *benchCell {
	for i := range rows {
		if rows[i].records == records && rows[i].payload == payload {
			return &rows[i]
		}
	}
	return nil
}

func writeHeatmap(outDir string, rows []benchCell, recordCounts []int, payloadSizes []int) error {
	nr, np := len(recordCounts), len(payloadSizes)
	grid := make([][]float64, nr)
	for i := range grid {
		grid[i] = make([]float64, np)
	}

	minR, maxR := math.MaxFloat64, -math.MaxFloat64
	for ri, nRec := range recordCounts {
		for pi, pay := range payloadSizes {
			c := findCell(rows, nRec, pay)
			if c == nil {
				grid[ri][pi] = math.NaN()
				continue
			}
			v := c.ratioPrettyOverCompact
			grid[ri][pi] = v
			if !math.IsNaN(v) && !math.IsInf(v, 0) {
				if v < minR {
					minR = v
				}
				if v > maxR {
					maxR = v
				}
			}
		}
	}

	cellW, cellH := 120, 40
	marginL, marginT := 180, 80
	imgW := marginL + np*cellW + 40
	imgH := marginT + nr*cellH + 80

	img := image.NewRGBA(image.Rect(0, 0, imgW, imgH))
	for y := 0; y < imgH; y++ {
		for x := 0; x < imgW; x++ {
			img.Set(x, y, color.RGBA{245, 245, 245, 255})
		}
	}

	for ri := 0; ri < nr; ri++ {
		for pi := 0; pi < np; pi++ {
			v := grid[ri][pi]
			var col color.RGBA
			if math.IsNaN(v) || math.IsInf(v, 0) {
				col = color.RGBA{200, 200, 200, 255}
			} else {
				t := (v - minR) / (maxR - minR + 1e-12)
				if t < 0 {
					t = 0
				}
				if t > 1 {
					t = 1
				}
				// low ratio blue (pretty ~= compact), high ratio green (compact wins big)
				col = color.RGBA{
					R: uint8(80 * (1 - t)),
					G: uint8(120 + 135*t),
					B: uint8(180 * (1 - t)),
					A: 255,
				}
			}
			x0 := marginL + pi*cellW
			y0 := marginT + ri*cellH
			for dy := 0; dy < cellH-2; dy++ {
				for dx := 0; dx < cellW-2; dx++ {
					img.Set(x0+dx, y0+dy, col)
				}
			}
		}
	}

	path := filepath.Join(outDir, "heatmap_ratio.png")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return err
	}
	fmt.Printf("Wrote %s (ratio pretty/compact; greener = larger speedup from compact)\n", path)
	return nil
}
