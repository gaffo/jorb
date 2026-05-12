// JSON run persistence: append-only JSONL for each job update plus asynchronous full checkpoints to the
// main checkpoint file (atomic rename). This file implements [JsonSerializer] and [Serializer].
//
// Why we chose this approach (see examples/serializerbench; per-write samples on a typical Linux worker):
//
//   - Incremental JSONL lines ([JsonSerializer.JobUpdate]; optional [JsonSerializerConfig.SyncAppend]
//     to fsync each line) stay on the order of ~1–3 ms average and low single-digit ms P90 per write for one job line.
//   - Rewriting the entire run to JSON on every completion scales with total run size (job count × payload),
//     so hot-path full-run snapshots quickly reach tens to hundreds of milliseconds per write on large runs.
//
// So completion-time work stays bounded by small line appends; full-run JSON cost is confined to
// background checkpoints and to explicit CheckpointSync/Close.
//
// Shutdown and Ctrl-C: call [JsonSerializer.Close] when stopping [Processor.Exec] (or from a signal
// handler). Close stops the checkpoint worker, runs a final synchronous checkpoint so all state is
// merged into the main checkpoint file and incremental segment files are cleared—equivalent to flush/finalize.
// Relying on process exit without Close leaves the last JSONL segments plus checkpoint for replay (still
// consistent), but Close avoids relying on replay alone after a clean stop.
package jorb

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const jsonlRecordVersion = 1

// JsonSerializerConfig configures JSONL append plus asynchronous checkpoint behavior.
type JsonSerializerConfig struct {
	// CheckpointInterval runs a full checkpoint on this interval when > 0 (in addition to checkpoints scheduled after appends).
	CheckpointInterval time.Duration
	// SyncAppend fsyncs the append file after each JobUpdate (slower, safer).
	SyncAppend bool
}

// JsonSerializer implements [Serializer] with durability via JSONL plus periodic full checkpoints.
// Construct with [NewJsonSerializer]; the returned *Run must be the same instance passed to [Processor.Exec].
type JsonSerializer[OC, JC any] struct {
	checkpointPath string
	dir            string
	stem           string
	appendPath     string

	cfg JsonSerializerConfig

	run *Run[OC, JC]

	appendMu   sync.Mutex
	appendFile *os.File
	sealSeq    uint64

	ckMu              sync.Mutex
	checkpointBusy    bool
	checkpointPending bool

	stop chan struct{}
	done chan struct{}

	ticker *time.Ticker
	kick   chan struct{}
}

// incrementalStemPaths derives sibling JSONL paths from the checkpoint file path.
// For checkpoint `.../run.json` → active `.../run.incremental.jsonl`, sealed `.../run.incremental.sealed.%06d.jsonl`.
func incrementalStemPaths(checkpointPath string) (dir, stem, appendPath string) {
	dir = filepath.Dir(checkpointPath)
	base := filepath.Base(checkpointPath)
	ext := filepath.Ext(base)
	if ext != "" {
		stem = strings.TrimSuffix(base, ext)
	} else {
		stem = base
	}
	appendPath = filepath.Join(dir, stem+".incremental.jsonl")
	return dir, stem, appendPath
}

func sealedIncrementalPath(dir, stem string, seq uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%s.incremental.sealed.%06d.jsonl", stem, seq))
}

// legacyAppendPath is the older active-segment filename on disk (replay and deletion only; new runs use appendPath).
func legacyAppendPath(dir, stem string) string {
	return filepath.Join(dir, stem+".wal.jsonl")
}

// NewJsonSerializer loads checkpoint + replays JSONL segments, returns the store and the restored Run.
func NewJsonSerializer[OC, JC any](checkpointPath string, cfg JsonSerializerConfig) (*JsonSerializer[OC, JC], *Run[OC, JC], error) {
	dir, stem, appendPath := incrementalStemPaths(checkpointPath)
	if err := os.MkdirAll(dir, 0600); err != nil {
		return nil, nil, err
	}

	w := &JsonSerializer[OC, JC]{
		checkpointPath: checkpointPath,
		dir:            dir,
		stem:           stem,
		appendPath:     appendPath,
		cfg:            cfg,
		sealSeq:        0,
		stop:           make(chan struct{}),
		done:           make(chan struct{}),
		kick:           make(chan struct{}, 1),
	}

	if err := recoverCheckpointAtomic(checkpointPath); err != nil {
		return nil, nil, err
	}

	run, _, err := w.loadCheckpointAndReplay()
	if err != nil {
		return nil, nil, err
	}
	w.run = run
	if err := w.materializeCleanIncremental(); err != nil {
		return nil, nil, err
	}

	if err := w.openAppend(); err != nil {
		return nil, nil, err
	}

	if cfg.CheckpointInterval > 0 {
		w.ticker = time.NewTicker(cfg.CheckpointInterval)
	}
	go w.checkpointLoop()

	return w, run, nil
}

func recoverCheckpointAtomic(checkpointPath string) error {
	dir := filepath.Dir(checkpointPath)
	tmpPath := checkpointPath + ".tmp"
	oldPath := checkpointPath + ".old"

	// Prefer completed checkpoint over orphaned tmp.
	if _, err := os.Stat(checkpointPath); err == nil {
		_ = os.Remove(tmpPath)
		_ = os.Remove(oldPath)
		return nil
	}

	if _, err := os.Stat(tmpPath); err == nil {
		if _, err2 := os.Stat(checkpointPath); err2 != nil && os.IsNotExist(err2) {
			if err := os.Rename(tmpPath, checkpointPath); err != nil {
				return fmt.Errorf("recover checkpoint from tmp: %w", err)
			}
			return nil
		}
	}

	if _, err := os.Stat(oldPath); err == nil {
		if _, err2 := os.Stat(checkpointPath); err2 != nil && os.IsNotExist(err2) {
			if err := os.Rename(oldPath, checkpointPath); err != nil {
				return fmt.Errorf("recover checkpoint from old: %w", err)
			}
			_ = os.Remove(tmpPath)
			return nil
		}
	}

	_ = dir
	return nil
}

func (w *JsonSerializer[OC, JC]) loadCheckpointAndReplay() (*Run[OC, JC], uint64, error) {
	var run *Run[OC, JC]
	if _, err := os.Stat(w.checkpointPath); err != nil {
		if os.IsNotExist(err) {
			var oc OC
			run = NewRun[OC, JC]("default", oc)
		} else {
			return nil, 0, err
		}
	} else {
		data, err := os.ReadFile(w.checkpointPath)
		if err != nil {
			return nil, 0, err
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		var r Run[OC, JC]
		if err := dec.Decode(&r); err != nil {
			return nil, 0, err
		}
		r.Init()
		run = &r
	}

	sealed, maxSeq, err := w.listSealedSegments()
	if err != nil {
		return nil, 0, err
	}
	for _, path := range sealed {
		if err := replayJSONLFile(path, run); err != nil {
			return nil, 0, fmt.Errorf("replay %s: %w", path, err)
		}
	}
	if _, err := os.Stat(w.appendPath); err == nil {
		if err := replayJSONLFile(w.appendPath, run); err != nil {
			return nil, 0, fmt.Errorf("replay active append log: %w", err)
		}
	} else if _, err := os.Stat(legacyAppendPath(w.dir, w.stem)); err == nil {
		if err := replayJSONLFile(legacyAppendPath(w.dir, w.stem), run); err != nil {
			return nil, 0, fmt.Errorf("replay legacy append log: %w", err)
		}
	}

	return run, maxSeq, nil
}

func (w *JsonSerializer[OC, JC]) listSealedSegments() ([]string, uint64, error) {
	// Include older on-disk segment names so existing state directories still replay.
	patterns := []string{
		filepath.Join(w.dir, w.stem+".incremental.sealed.*.jsonl"),
		filepath.Join(w.dir, w.stem+".wal.sealed.*.jsonl"),
	}
	seen := map[string]struct{}{}
	var matches []string
	for _, pattern := range patterns {
		m, err := filepath.Glob(pattern)
		if err != nil {
			return nil, 0, err
		}
		for _, path := range m {
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			matches = append(matches, path)
		}
	}
	type pair struct {
		seq  uint64
		path string
	}
	var pairs []pair
	var max uint64
	for _, m := range matches {
		seq, ok := parseSealedSeq(filepath.Base(m))
		if !ok {
			continue
		}
		pairs = append(pairs, pair{seq, m})
		if seq > max {
			max = seq
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].seq < pairs[j].seq })
	out := make([]string, len(pairs))
	for i := range pairs {
		out[i] = pairs[i].path
	}
	return out, max, nil
}

func parseSealedSeq(base string) (uint64, bool) {
	const inc = ".incremental.sealed."
	const legacySealed = ".wal.sealed."
	var rest string
	switch {
	case strings.Contains(base, inc):
		i := strings.Index(base, inc)
		rest = base[i+len(inc):]
	case strings.Contains(base, legacySealed):
		i := strings.Index(base, legacySealed)
		rest = base[i+len(legacySealed):]
	default:
		return 0, false
	}
	j := strings.IndexByte(rest, '.')
	if j < 0 {
		return 0, false
	}
	n, err := strconv.ParseUint(rest[:j], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func replayJSONLFile[OC, JC any](path string, run *Run[OC, JC]) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	const maxScan = 256 << 20
	buf := make([]byte, 0, 64<<10)
	sc.Buffer(buf, maxScan)
	lineNum := 0
	for sc.Scan() {
		lineNum++
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if err := applyJSONLLine[OC, JC](line, run); err != nil {
			return fmt.Errorf("line %d: %w", lineNum, err)
		}
	}
	return sc.Err()
}

func applyJSONLLine[OC, JC any](line []byte, run *Run[OC, JC]) error {
	var rec struct {
		V   int     `json:"v"`
		Op  string  `json:"op"`
		Job Job[JC] `json:"job"`
	}
	if err := json.Unmarshal(line, &rec); err != nil {
		return err
	}
	if rec.V != jsonlRecordVersion {
		return fmt.Errorf("unsupported JSONL record version %d", rec.V)
	}
	switch rec.Op {
	case "job":
		run.UpdateJob(rec.Job)
	default:
		return fmt.Errorf("unknown op %q", rec.Op)
	}
	return nil
}

// materializeCleanIncremental writes a checkpoint from current memory and removes all incremental
// segment files on disk so the next session cannot double-replay segments.
func (w *JsonSerializer[OC, JC]) materializeCleanIncremental() error {
	snap := w.snapshotRun()
	if err := w.writeCheckpointAtomic(snap); err != nil {
		return err
	}
	return w.removeAllAppendArtifacts()
}

func (w *JsonSerializer[OC, JC]) openAppend() error {
	w.appendMu.Lock()
	defer w.appendMu.Unlock()
	f, err := os.OpenFile(w.appendPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	w.appendFile = f
	return nil
}

// JobUpdate appends one JSON object per line (hot path) and schedules an asynchronous full checkpoint
// when the append succeeds (coalesced with other work). scheduleCheckpoint runs after [appendMu] is released
// so the checkpoint goroutine never blocks on the append lock.
func (w *JsonSerializer[OC, JC]) JobUpdate(job Job[JC]) error {
	w.appendMu.Lock()
	if w.appendFile == nil {
		w.appendMu.Unlock()
		return errors.New("json serializer closed")
	}
	enc := json.NewEncoder(w.appendFile)
	enc.SetEscapeHTML(false)
	rec := struct {
		V   int     `json:"v"`
		Op  string  `json:"op"`
		Job Job[JC] `json:"job"`
	}{V: jsonlRecordVersion, Op: "job", Job: job}
	if err := enc.Encode(rec); err != nil {
		w.appendMu.Unlock()
		return err
	}
	if w.cfg.SyncAppend {
		if err := w.appendFile.Sync(); err != nil {
			w.appendMu.Unlock()
			return err
		}
	}
	w.appendMu.Unlock()
	if w.ticker == nil {
		w.scheduleCheckpoint()
	}
	return nil
}

func (w *JsonSerializer[OC, JC]) scheduleCheckpoint() {
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

func (w *JsonSerializer[OC, JC]) checkpointLoop() {
	defer close(w.done)
	for {
		if w.ticker != nil {
			select {
			case <-w.stop:
				return
			case <-w.ticker.C:
				w.runCheckpoint()
			case <-w.kick:
				w.runCheckpoint()
			}
		} else {
			select {
			case <-w.stop:
				return
			case <-w.kick:
				w.runCheckpoint()
			}
		}
	}
}

func (w *JsonSerializer[OC, JC]) runCheckpoint() {
	w.ckMu.Lock()
	if w.checkpointBusy {
		w.checkpointPending = true
		w.ckMu.Unlock()
		return
	}
	w.checkpointBusy = true
	w.ckMu.Unlock()

	err := w.doCheckpointRotateWriteDelete()

	w.ckMu.Lock()
	w.checkpointBusy = false
	pending := w.checkpointPending
	w.checkpointPending = false
	w.ckMu.Unlock()

	if err != nil {
		slog.Error("checkpoint failed", "err", err)
	}
	if pending {
		select {
		case w.kick <- struct{}{}:
		default:
		}
	}
}

// CheckpointSync performs rotate + snapshot + publish synchronously (explicit flush; normal path uses append-driven checkpoints).
func (w *JsonSerializer[OC, JC]) CheckpointSync() error {
	return w.doCheckpointRotateWriteDelete()
}

func (w *JsonSerializer[OC, JC]) doCheckpointRotateWriteDelete() error {
	// Single appendMu critical section from append file close through reopen so JobUpdate never observes a nil
	// handle between rotate and recreate (that gap caused spurious "json serializer closed" races).
	w.appendMu.Lock()
	defer w.appendMu.Unlock()

	sealedPath := sealedIncrementalPath(w.dir, w.stem, w.sealSeq+1)

	if w.appendFile != nil {
		_ = w.appendFile.Close()
		w.appendFile = nil
	}
	if _, err := os.Stat(w.appendPath); err == nil {
		if err := os.Rename(w.appendPath, sealedPath); err != nil {
			return fmt.Errorf("rotate append log: %w", err)
		}
		w.sealSeq++
	}

	snap := w.snapshotRun()
	if err := w.writeCheckpointAtomic(snap); err != nil {
		return err
	}

	if err := w.removeAllAppendArtifactsLocked(); err != nil {
		return err
	}
	f, err := os.OpenFile(w.appendPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	w.appendFile = f
	return nil
}

func (w *JsonSerializer[OC, JC]) removeAllAppendArtifacts() error {
	w.appendMu.Lock()
	defer w.appendMu.Unlock()
	return w.removeAllAppendArtifactsLocked()
}

func (w *JsonSerializer[OC, JC]) removeAllAppendArtifactsLocked() error {
	// Remove current and legacy segment filenames under stem.
	for _, pattern := range []string{
		filepath.Join(w.dir, w.stem+".incremental*"),
		filepath.Join(w.dir, w.stem+".wal*"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		for _, m := range matches {
			if err := os.Remove(m); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	w.sealSeq = 0
	return nil
}

func (w *JsonSerializer[OC, JC]) snapshotRun() Run[OC, JC] {
	w.run.m.Lock()
	defer w.run.m.Unlock()
	jobs := make(map[string]Job[JC], len(w.run.Jobs))
	for k, v := range w.run.Jobs {
		jobs[k] = v
	}
	return Run[OC, JC]{
		Name:    w.run.Name,
		Jobs:    jobs,
		Overall: w.run.Overall,
	}
}

func (w *JsonSerializer[OC, JC]) writeCheckpointAtomic(run Run[OC, JC]) error {
	tmpPath := w.checkpointPath + ".tmp"
	dir := filepath.Dir(w.checkpointPath)
	if err := os.MkdirAll(dir, 0600); err != nil {
		return err
	}
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(run); err != nil {
		f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, w.checkpointPath); err != nil {
		return err
	}
	slog.Debug("checkpoint written", "file", w.checkpointPath)
	return nil
}

// Deserialize reloads checkpoint + JSONL segments from disk and replaces the store's live [Run] (same
// pointer the processor must use with [Processor.Exec] so appends snapshot the right in-memory state).
func (w *JsonSerializer[OC, JC]) Deserialize() (*Run[OC, JC], error) {
	run, _, err := w.loadCheckpointAndReplay()
	if err != nil {
		return nil, err
	}
	run.Init()
	w.run = run
	return run, nil
}

// Close stops the background checkpoint worker, performs a final [JsonSerializer.CheckpointSync] so
// the live run is written atomically to the main checkpoint file and all JSONL segment files are
// removed, then closes the active append handle. Always call Close after processing completes or when
// handling SIGINT/SIGTERM so on-disk state is fully merged after a clean shutdown.
var _ Serializer[any, any] = (*JsonSerializer[any, any])(nil)

func (w *JsonSerializer[OC, JC]) Close() error {
	if w.ticker != nil {
		w.ticker.Stop()
	}
	close(w.stop)
	<-w.done

	if err := w.CheckpointSync(); err != nil {
		w.appendMu.Lock()
		if w.appendFile != nil {
			_ = w.appendFile.Close()
			w.appendFile = nil
		}
		w.appendMu.Unlock()
		return fmt.Errorf("final checkpoint on close: %w", err)
	}

	w.appendMu.Lock()
	defer w.appendMu.Unlock()
	if w.appendFile != nil {
		err := w.appendFile.Close()
		w.appendFile = nil
		return err
	}
	return nil
}
