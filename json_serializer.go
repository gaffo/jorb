// JSON run persistence: append-only JSONL for each job update plus asynchronous full checkpoints to the
// main checkpoint file (atomic rename). This file implements [JsonSerializer] and [Serializer].
//
// # Architecture Overview
//
// JsonSerializer maintains durable job state through two mechanisms:
//
//  1. Incremental JSONL appends (.incremental.jsonl) - Hot path for JobUpdate
//  2. Full checkpoint snapshots (.json) - Cold path consolidation
//
// # Write Path (JobUpdate)
//
// Each JobUpdate appends a single JSONL line to the active .incremental.jsonl file
// and fsyncs it for durability.
//
// # Checkpoint Path (Background)
//
// Checkpoints run asynchronously in checkpointLoop, triggered by:
//   - Timer (CheckpointInterval > 0)
//   - Per-append kick (CheckpointInterval == 0, coalesced when busy)
//
// Checkpoint algorithm (atomic 3-phase):
//  1. Rotate: Close active .incremental.jsonl → sealed .incremental.sealed.NNNNNN.jsonl
//  2. Snapshot: Lock Run.m, copy Jobs map, write full state to .json.tmp
//  3. Publish: Rename .json.tmp → .json, delete all sealed segments
//
// The atomic rename ensures checkpoint files are never partially written.
// On restart, recoverCheckpointAtomic handles recovery from .tmp or .old files if needed.
//
// # Recovery Path (NewJsonSerializer)
//
// On startup:
//  1. Recover checkpoint from .tmp/.old if .json is missing (crash recovery)
//  2. Load main checkpoint .json (if exists)
//  3. Replay sealed .incremental.sealed.*.jsonl in order
//  4. Replay active .incremental.jsonl
//  5. Write clean checkpoint, delete all segments (materializeCleanIncremental)
//
// # Checkpoint Failure Handling
//
// If checkpoints fail (e.g., disk full), runCheckpoint implements exponential backoff:
//   - 1s, 2s, 4s, 8s, max 30s between retries
//   - Incremental appends continue during backoff (no data loss)
//   - Recovery relies on replaying accumulated JSONL segments
//
// # Performance Characteristics
//
// Per-write samples on a typical Linux worker:
//   - Incremental JSONL append with fsync: ~1-3ms average, low single-digit ms P90
//   - Full checkpoint scales with job count × payload size (tens to hundreds of ms)
//
// Hot-path work (JobUpdate) stays bounded by small line appends; full-run checkpoint cost
// is confined to background checkpoint goroutine and explicit CheckpointSync/Close calls.
//
// # Shutdown and Ctrl-C
//
// Call [JsonSerializer.Close] when stopping [Processor.Exec] (or from a signal handler).
// Close stops the checkpoint worker, runs a final synchronous checkpoint so all state is
// merged into the main checkpoint file and incremental segment files are cleared—equivalent
// to flush/finalize. Relying on process exit without Close leaves the last JSONL segments
// plus checkpoint for replay (still consistent), but Close avoids relying on replay alone
// after a clean stop.
//
// # Why This Approach
//
// Measured alternatives comparing performance characteristics:
//   - Incremental JSONL lines ([JsonSerializer.JobUpdate]) stay ~1-3ms avg with fsync
//   - Rewriting entire run to JSON on every completion scales with total run size,
//     reaching tens to hundreds of milliseconds per write on large runs
//
// So completion-time work stays bounded by small line appends; full-run JSON cost is
// confined to background checkpoints and explicit CheckpointSync/Close.
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

// jsonSerializerConfig configures JSONL append plus asynchronous checkpoint behavior.
//
// # Checkpoint Strategy
//
// JsonSerializer uses a dual-persistence model:
//   - Hot path: Fast JSONL appends to .incremental.jsonl for each JobUpdate
//   - Cold path: Periodic full checkpoint snapshots to the main .json file
//
// CheckpointInterval controls when full checkpoints occur. There are two checkpoint triggers:
//  1. Time-based (CheckpointInterval > 0): Runs checkpoint on a timer
//  2. Per-append (CheckpointInterval == 0): Schedules checkpoint after each JobUpdate (coalesced)
//
// MaxReplayLineBytes caps individual JSONL line size during replay (default 256MB).
// Increase if you have very large job contexts; decrease to limit memory on constrained systems.
//
// For development/testing (no persistence needed):
//
//	Use NilSerializer instead
type jsonSerializerConfig struct {
	// CheckpointInterval runs a full checkpoint on this interval when > 0.
	// When 0, checkpoints are scheduled after each JobUpdate (coalesced if multiple updates occur rapidly).
	// Checkpoints merge all incremental JSONL files into the main checkpoint file.
	CheckpointInterval time.Duration


	// MaxReplayLineBytes caps individual JSONL line size during replay (default 256MB if zero).
	// Increase for large job contexts; decrease to limit memory usage on constrained systems.
	MaxReplayLineBytes int
}

// JsonSerializer implements [Serializer] with durability via JSONL plus periodic full checkpoints.
// Construct with [NewJsonSerializer]; the returned *Run must be the same instance passed to [Processor.Exec].
type JsonSerializer[OC, JC any] struct {
	checkpointPath string
	dir            string
	stem           string
	appendPath     string

	cfg jsonSerializerConfig

	run *Run[OC, JC]

	appendMu   sync.Mutex
	appendFile *os.File
	sealSeq    uint64

	ckMu              sync.Mutex
	checkpointBusy    bool
	checkpointPending bool
	ckFailures        int       // consecutive checkpoint failures for backoff
	ckBackoffUntil    time.Time // skip checkpoints until this time

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

// NewJsonSerializer loads checkpoint + replays JSONL segments, returns the store and the restored Run.
func NewJsonSerializer[OC, JC any](checkpointPath string) (*JsonSerializer[OC, JC], *Run[OC, JC], error) {
	dir, stem, appendPath := incrementalStemPaths(checkpointPath)
	if err := os.MkdirAll(dir, 0600); err != nil {
		return nil, nil, err
	}

	w := &JsonSerializer[OC, JC]{
		checkpointPath: checkpointPath,
		dir:            dir,
		stem:           stem,
		appendPath:     appendPath,
		cfg: jsonSerializerConfig{
			CheckpointInterval: time.Second * 10,
		},
		sealSeq: 0,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		kick:    make(chan struct{}, 1),
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

	if w.cfg.CheckpointInterval > 0 {
		w.ticker = time.NewTicker(w.cfg.CheckpointInterval)
	}
	go w.checkpointLoop()

	return w, run, nil
}

// recoverCheckpointAtomic handles crash recovery for the atomic checkpoint protocol.
//
// Checkpoint files follow the pattern:
//   - checkpoint.json: The authoritative checkpoint (atomically renamed from .tmp)
//   - checkpoint.json.tmp: In-progress checkpoint being written
//   - checkpoint.json.old: Previous checkpoint (backup during rename, cleaned after)
//
// Recovery logic:
//  1. If .json exists → Normal case, clean up any orphaned .tmp/.old
//  2. If .json missing + .tmp exists → Crash during rename, promote .tmp to .json
//  3. If .json missing + .old exists → Crash before cleanup, restore .old to .json
//  4. If none exist → Fresh start (NewJsonSerializer will initialize empty Run)
//
// This ensures we never lose a checkpoint: either the new one completed (.json),
// or we can recover from in-progress (.tmp) or previous (.old) state.
func recoverCheckpointAtomic(checkpointPath string) error {
	tmpPath := checkpointPath + ".tmp"
	oldPath := checkpointPath + ".old"

	// Check if main checkpoint exists
	_, errMain := os.Stat(checkpointPath)
	mainExists := errMain == nil

	// Prefer completed checkpoint over orphaned tmp or old.
	if mainExists {
		_ = os.Remove(tmpPath)
		_ = os.Remove(oldPath)
		return nil
	}

	// No main checkpoint exists; try to recover from tmp or old.
	if !os.IsNotExist(errMain) {
		return errMain // Unexpected stat error
	}

	// Try tmp file first
	if _, err := os.Stat(tmpPath); err == nil {
		if err := os.Rename(tmpPath, checkpointPath); err != nil {
			return fmt.Errorf("recover checkpoint from tmp: %w", err)
		}
		_ = os.Remove(oldPath)
		return nil
	}

	// Try old file
	if _, err := os.Stat(oldPath); err == nil {
		if err := os.Rename(oldPath, checkpointPath); err != nil {
			return fmt.Errorf("recover checkpoint from old: %w", err)
		}
		_ = os.Remove(tmpPath)
		return nil
	}

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
	maxLineBytes := w.cfg.MaxReplayLineBytes
	if maxLineBytes == 0 {
		maxLineBytes = 256 << 20 // 256MB default
	}
	for _, path := range sealed {
		if err := replayJSONLFile(path, run, maxLineBytes); err != nil {
			return nil, 0, fmt.Errorf("replay %s: %w", path, err)
		}
	}
	if _, err := os.Stat(w.appendPath); err == nil {
		if err := replayJSONLFile(w.appendPath, run, maxLineBytes); err != nil {
			return nil, 0, fmt.Errorf("replay active append log: %w", err)
		}
	}

	return run, maxSeq, nil
}

func (w *JsonSerializer[OC, JC]) listSealedSegments() ([]string, uint64, error) {
	// Include older on-disk segment names so existing state directories still replay.
	patterns := []string{
		filepath.Join(w.dir, w.stem+".incremental.sealed.*.jsonl"),
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
	if !strings.Contains(base, inc) {
		return 0, false
	}
	i := strings.Index(base, inc)
	rest := base[i+len(inc):]
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

func replayJSONLFile[OC, JC any](path string, run *Run[OC, JC], maxLineBytes int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64<<10)
	sc.Buffer(buf, maxLineBytes)
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
	if err := w.appendFile.Sync(); err != nil {
		w.appendMu.Unlock()
		return err
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

// runCheckpoint executes checkpoint with exponential backoff on failure.
//
// Backoff schedule: 1s, 2s, 4s, 8s, 16s, max 30s between retries.
// During backoff, incremental appends continue normally (no data loss).
// On success, backoff resets. Pending checkpoints reschedule via kick channel.
//
// If checkpoint fails repeatedly (e.g., disk full), backoff prevents tight retry loops
// while allowing the system to continue appending. Recovery relies on replaying
// accumulated JSONL segments when the issue resolves.
func (w *JsonSerializer[OC, JC]) runCheckpoint() {
	w.ckMu.Lock()
	// Check if we're in backoff
	if time.Now().Before(w.ckBackoffUntil) {
		w.checkpointPending = true
		w.ckMu.Unlock()
		return
	}
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
	if err != nil {
		w.ckFailures++
		// Exponential backoff: 1s, 2s, 4s, 8s, max 30s
		backoffSec := 1 << min(w.ckFailures-1, 4)
		if backoffSec > 30 {
			backoffSec = 30
		}
		w.ckBackoffUntil = time.Now().Add(time.Duration(backoffSec) * time.Second)
		slog.Error("checkpoint failed, backing off", "err", err, "failures", w.ckFailures, "backoffSec", backoffSec)
	} else {
		w.ckFailures = 0
		w.ckBackoffUntil = time.Time{}
	}
	pending := w.checkpointPending
	w.checkpointPending = false
	w.ckMu.Unlock()

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

// doCheckpointRotateWriteDelete executes the 3-phase atomic checkpoint protocol:
//
// Phase 1 - Rotate: Close active .incremental.jsonl and rename to sealed segment
//
//	This freezes the current append log so new writes go to a fresh file
//
// Phase 2 - Snapshot: Lock Run, copy Jobs map, write full state to .json.tmp
//
//	Snapshot runs under Run.m lock but releases before fsync to avoid blocking
//
// Phase 3 - Publish: Atomic rename .json.tmp → .json, then delete sealed segments
//
//	The rename makes the checkpoint visible atomically; cleanup is safe after
//
// The entire operation holds appendMu to prevent JobUpdate from seeing nil appendFile
// between rotate and reopen. This eliminates "json serializer closed" races.
//
// If any phase fails, sealed segments remain on disk and will be replayed on restart.
func (w *JsonSerializer[OC, JC]) doCheckpointRotateWriteDelete() error {
	// Single appendMu critical section from append file close through reopen so JobUpdate never observes a nil
	// handle between rotate and recreate (that gap caused spurious "json serializer closed" races).
	w.appendMu.Lock()
	defer w.appendMu.Unlock()

	sealedPath := sealedIncrementalPath(w.dir, w.stem, w.sealSeq+1)

	if w.appendFile != nil {
		if err := w.appendFile.Close(); err != nil {
			slog.Error("failed to close append file during checkpoint rotation", "err", err)
		}
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
	// Remove incremental segment files under stem.
	pattern := filepath.Join(w.dir, w.stem+".incremental*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	for _, m := range matches {
		if err := os.Remove(m); err != nil && !os.IsNotExist(err) {
			return err
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
			closeErr := w.appendFile.Close()
			w.appendFile = nil
			w.appendMu.Unlock()
			if closeErr != nil {
				return fmt.Errorf("final checkpoint on close: %w (also failed to close append file: %v)", err, closeErr)
			}
		} else {
			w.appendMu.Unlock()
		}
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
