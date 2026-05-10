// Command serializerbench measures per-write latency (mean + P90) for JsonSerializer.JobUpdate.
// For each grid cell (records × payload_bytes), it performs total_writes = records × write_multiplier
// timed append operations (default multiplier 10).
//
// Run: go run . -quiet -out ./out
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/gaffo/jorb"
	"github.com/schollz/progressbar/v3"
)

func main() {
	outDir := flag.String("out", "serializerbench_out", "output directory for CSV")
	maxProduct := flag.Int64("max-product-bytes", 200<<20, "skip cells when records×payload exceeds this")
	writeMult := flag.Int("writes-mult", 10, "total writes per cell = records × this (before cap)")
	maxWrites := flag.Int("max-writes", 0, "if > 0, cap total writes per cell at this (0 = unlimited)")
	syncAppend := flag.Bool("sync-append", true, "JsonSerializer SyncAppend (fsync each line)")
	quiet := flag.Bool("quiet", false, "suppress progress bar")
	recordCountsStr := flag.String("records", "10,100", "comma-separated job counts")
	payloadKBStr := flag.String("payload-kib", "100,400", "comma-separated payload sizes per job (KiB)")
	flag.Parse()

	recordCounts, err := parseIntList(*recordCountsStr)
	if err != nil {
		log.Fatal(err)
	}
	payloads, err := parsePayloadKiB(*payloadKBStr)
	if err != nil {
		log.Fatal(err)
	}

	if err := os.MkdirAll(*outDir, 0750); err != nil {
		log.Fatal(err)
	}

	type pair struct{ nRec, pay int }
	var pairs []pair
	var skipped int
	for _, nRec := range recordCounts {
		for _, pay := range payloads {
			if int64(nRec)*int64(pay) > *maxProduct {
				skipped++
				continue
			}
			pairs = append(pairs, pair{nRec, pay})
		}
	}
	if skipped > 0 {
		log.Printf("skipped %d cells over -max-product-bytes=%d\n", skipped, *maxProduct)
	}
	if len(pairs) == 0 {
		log.Fatal("no grid cells to run")
	}

	tmpRoot, err := os.MkdirTemp("", "serializerbench-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(tmpRoot)

	var rows []benchRow
	totalTimedOps := 0
	for _, q := range pairs {
		nw := q.nRec * *writeMult
		if *maxWrites > 0 && nw > *maxWrites {
			nw = *maxWrites
		}
		totalTimedOps += nw
	}

	var bar *progressbar.ProgressBar
	if !*quiet {
		bar = progressbar.NewOptions(totalTimedOps,
			progressbar.OptionSetDescription("append samples"),
			progressbar.OptionShowCount(),
			progressbar.OptionSetWidth(40),
			progressbar.OptionClearOnFinish(),
		)
	}

	for cellIdx, q := range pairs {
		nRec, pay := q.nRec, q.pay
		totalWrites := nRec * *writeMult
		capped := false
		if *maxWrites > 0 && totalWrites > *maxWrites {
			totalWrites = *maxWrites
			capped = true
		}

		dir := filepath.Join(tmpRoot, fmt.Sprintf("cell_%d", cellIdx))
		if err := os.MkdirAll(dir, 0750); err != nil {
			log.Fatal(err)
		}

		run := buildRun(nRec, pay)
		rp := &run

		samples := benchAppendSamples(rp, dir, jorb.JsonSerializerConfig{SyncAppend: *syncAppend}, totalWrites, bar)

		rows = append(rows, benchRow{
			records:      nRec,
			payload:      pay,
			totalWrites:  totalWrites,
			writesCapped: capped,
			appendAvg:    mean(samples),
			appendP90:    p90(samples),
		})
	}

	if bar != nil {
		_ = bar.Finish()
	}

	csvPath := filepath.Join(*outDir, "results.csv")
	if err := writeCSV(csvPath, rows); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Wrote %s\n", csvPath)

	printTable(rows)
}

type benchRow struct {
	records      int
	payload      int
	totalWrites  int
	writesCapped bool
	appendAvg    time.Duration
	appendP90    time.Duration
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

func jobsInKeyOrder(run *jorb.Run[jorb.MyOverallContext, jorb.MyJobContext]) []jorb.Job[jorb.MyJobContext] {
	ids := make([]string, 0, len(run.Jobs))
	for id := range run.Jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]jorb.Job[jorb.MyJobContext], 0, len(ids))
	for _, id := range ids {
		out = append(out, run.Jobs[id])
	}
	return out
}

func benchAppendSamples(
	run *jorb.Run[jorb.MyOverallContext, jorb.MyJobContext],
	dir string,
	cfg jorb.JsonSerializerConfig,
	nWrites int,
	bar *progressbar.ProgressBar,
) []time.Duration {
	cp := filepath.Join(dir, "bench_state.json")
	ws, _, err := jorb.NewJsonSerializer[jorb.MyOverallContext, jorb.MyJobContext](cp, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer ws.Close()

	jobs := jobsInKeyOrder(run)
	out := make([]time.Duration, 0, nWrites)
	for i := 0; i < nWrites; i++ {
		j := jobs[i%len(jobs)]
		j.C.Count = i
		start := time.Now()
		if err := ws.JobUpdate(j); err != nil {
			log.Fatal(err)
		}
		out = append(out, time.Since(start))
		if bar != nil {
			_ = bar.Add(1)
		}
	}
	return out
}

func mean(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	var sum int64
	for _, x := range d {
		sum += x.Nanoseconds()
	}
	return time.Duration(sum / int64(len(d)))
}

func p90(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return percentileNearestRank(s, 0.90)
}

func percentileNearestRank(sorted []time.Duration, p float64) time.Duration {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	k := int(math.Ceil(p * float64(n)))
	if k < 1 {
		k = 1
	}
	if k > n {
		k = n
	}
	return sorted[k-1]
}

func writeCSV(path string, rows []benchRow) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	line := "records,payload_bytes,total_writes,writes_capped,append_avg_ns,append_p90_ns,append_avg_ms,append_p90_ms\n"
	if _, err := f.WriteString(line); err != nil {
		return err
	}
	for _, r := range rows {
		capStr := "false"
		if r.writesCapped {
			capStr = "true"
		}
		_, err := fmt.Fprintf(f, "%d,%d,%d,%s,%d,%d,%g,%g\n",
			r.records, r.payload, r.totalWrites, capStr,
			r.appendAvg.Nanoseconds(), r.appendP90.Nanoseconds(),
			ms(r.appendAvg), ms(r.appendP90),
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func ms(d time.Duration) float64 {
	return float64(d.Nanoseconds()) / 1e6
}

func printTable(rows []benchRow) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "records\tpayload_B\ttotal_writes\tcapped\tappend_avg_ms\tappend_p90_ms")
	for _, r := range rows {
		capS := "-"
		if r.writesCapped {
			capS = "yes"
		}
		fmt.Fprintf(w, "%d\t%d\t%d\t%s\t%.3f\t%.3f\n",
			r.records, r.payload, r.totalWrites, capS,
			ms(r.appendAvg), ms(r.appendP90),
		)
	}
	_ = w.Flush()
}

func parseIntList(s string) ([]int, error) {
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("records: %w", err)
		}
		out = append(out, n)
	}
	return out, nil
}

func parsePayloadKiB(s string) ([]int, error) {
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		kib, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("payload-kib: %w", err)
		}
		out = append(out, kib<<10)
	}
	return out, nil
}
