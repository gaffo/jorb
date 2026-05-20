package jorb

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewJsonSerializer_FreshDir(t *testing.T) {
	dir := t.TempDir()
	cp := filepath.Join(dir, "state.json")

	ws, run, err := NewJsonSerializer[MyOverallContext, MyJobContext](cp, JsonSerializerConfig{})
	require.NoError(t, err)
	defer ws.Close()

	assert.NotNil(t, run)
	_, err = os.Stat(cp)
	require.NoError(t, err, "materialize should write checkpoint")
}

func TestJsonSerializer_ReplayOnRestart(t *testing.T) {
	dir := t.TempDir()
	cp := filepath.Join(dir, "state.json")

	ws, run, err := NewJsonSerializer[MyOverallContext, MyJobContext](cp, JsonSerializerConfig{})
	require.NoError(t, err)

	j := Job[MyJobContext]{
		Id:          "0",
		State:       "running",
		C:           MyJobContext{Name: "x"},
		StateErrors: map[string][]string{},
	}
	run.UpdateJob(j)
	require.NoError(t, ws.JobUpdate(j))
	require.NoError(t, ws.Close())

	ws2, run2, err := NewJsonSerializer[MyOverallContext, MyJobContext](cp, JsonSerializerConfig{})
	require.NoError(t, err)
	defer ws2.Close()

	got, ok := run2.Jobs["0"]
	require.True(t, ok)
	assert.Equal(t, "running", got.State)
	assert.Equal(t, "x", got.C.Name)
}

func TestJsonSerializer_CheckpointSyncClearsIncrementalArtifacts(t *testing.T) {
	dir := t.TempDir()
	cp := filepath.Join(dir, "state.json")

	ws, run, err := NewJsonSerializer[MyOverallContext, MyJobContext](cp, JsonSerializerConfig{})
	require.NoError(t, err)
	defer ws.Close()

	j := Job[MyJobContext]{Id: "0", State: "a", C: MyJobContext{}, StateErrors: map[string][]string{}}
	run.UpdateJob(j)
	require.NoError(t, ws.JobUpdate(j))

	dirEntries := globIncremental(dir, t)
	require.NotEmpty(t, dirEntries)

	require.NoError(t, ws.CheckpointSync())

	sealed, err := filepath.Glob(filepath.Join(dir, "*.incremental.sealed.*"))
	require.NoError(t, err)
	assert.Empty(t, sealed, "checkpoint should remove sealed JSONL segments")
	active := filepath.Join(dir, "state.incremental.jsonl")
	fi, err := os.Stat(active)
	require.NoError(t, err)
	assert.Equal(t, int64(0), fi.Size(), "active append file is reopened empty after checkpoint")
}

func globIncremental(dir string, t *testing.T) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "*.incremental*"))
	require.NoError(t, err)
	return m
}

func TestJsonSerializer_CloseFinalCheckpointClearsSealedSegments(t *testing.T) {
	dir := t.TempDir()
	cp := filepath.Join(dir, "state.json")

	ws, run, err := NewJsonSerializer[MyOverallContext, MyJobContext](cp, JsonSerializerConfig{})
	require.NoError(t, err)

	j := Job[MyJobContext]{Id: "0", State: "done", C: MyJobContext{Name: "flush"}, StateErrors: map[string][]string{}}
	run.UpdateJob(j)
	require.NoError(t, ws.JobUpdate(j))
	require.NoError(t, ws.Close())

	sealed, err := filepath.Glob(filepath.Join(dir, "*.incremental.sealed.*"))
	require.NoError(t, err)
	assert.Empty(t, sealed, "Close should finalize checkpoint and remove sealed JSONL segments")

	ws2, run2, err := NewJsonSerializer[MyOverallContext, MyJobContext](cp, JsonSerializerConfig{})
	require.NoError(t, err)
	defer ws2.Close()
	got := run2.Jobs["0"]
	assert.Equal(t, "done", got.State)
	assert.Equal(t, "flush", got.C.Name)
}

// TestJsonSerializer_JobUpdateMeanLatencyBudget checks hot-path JobUpdate stays within a practical
// ceiling for a medium-sized run (SyncAppend on).
func TestJsonSerializer_JobUpdateMeanLatencyBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("mean latency; run without -short")
	}
	const nJobs = 80
	const pay = 4096
	padding := strings.Repeat("y", pay)

	dir := t.TempDir()
	cp := filepath.Join(dir, "state.json")
	ws, run, err := NewJsonSerializer[MyOverallContext, MyJobContext](cp, JsonSerializerConfig{SyncAppend: true})
	require.NoError(t, err)
	defer ws.Close()

	for i := 0; i < nJobs; i++ {
		run.AddJob(MyJobContext{String: padding})
	}

	ids := make([]string, 0, len(run.Jobs))
	for id := range run.Jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	const samples = 25
	var sumAppend int64
	for i := 0; i < samples; i++ {
		id := ids[i%len(ids)]
		j := run.Jobs[id]
		j.C.Count = i
		run.UpdateJob(j)

		t0 := time.Now()
		require.NoError(t, ws.JobUpdate(j))
		sumAppend += time.Since(t0).Nanoseconds()
	}

	meanAppend := time.Duration(sumAppend / samples)

	const appendMeanBudget = 150 * time.Millisecond
	require.Less(t, meanAppend, appendMeanBudget,
		"append mean %v exceeds practical hot-path budget %v", meanAppend, appendMeanBudget)
}

func TestJsonSerializer_AsyncCheckpointCoalesces(t *testing.T) {
	dir := t.TempDir()
	cp := filepath.Join(dir, "state.json")

	ws, _, err := NewJsonSerializer[MyOverallContext, MyJobContext](cp, JsonSerializerConfig{
		CheckpointInterval: 50 * time.Millisecond,
	})
	require.NoError(t, err)
	defer ws.Close()

	for i := 0; i < 20; i++ {
		require.NoError(t, ws.JobUpdate(Job[MyJobContext]{
			Id: fmt.Sprintf("coalesce-%d", i), State: "s", C: MyJobContext{},
			StateErrors: map[string][]string{},
		}))
	}
	time.Sleep(200 * time.Millisecond)
	_, err = os.Stat(cp)
	require.NoError(t, err)
}
