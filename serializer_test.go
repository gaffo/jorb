package jorb

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJsonSerializer_CheckpointRoundTrip(t *testing.T) {
	t.Parallel()

	run := NewRun[MyOverallContext, MyJobContext]("test", MyOverallContext{Name: "overall"})
	for i := 0; i < 10; i++ {
		run.AddJob(MyJobContext{Count: 0, Name: fmt.Sprintf("job-%d", i)})
	}

	dir := t.TempDir()
	cp := filepath.Join(dir, "state.json")
	ws, diskRun, err := NewJsonSerializer[MyOverallContext, MyJobContext](cp, JsonSerializerConfig{})
	require.NoError(t, err)

	for _, j := range run.Jobs {
		diskRun.UpdateJob(j)
	}
	require.NoError(t, ws.CheckpointSync())
	require.NoError(t, ws.Close())

	ws2, restored, err := NewJsonSerializer[MyOverallContext, MyJobContext](cp, JsonSerializerConfig{})
	require.NoError(t, err)
	defer ws2.Close()

	assert.True(t, diskRun.Equal(restored))
}

func TestJsonSerializer_CheckpointWithJobErrors(t *testing.T) {
	t.Parallel()

	tempDir, err := os.MkdirTemp("", "test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	want := NewRun[MyOverallContext, MyJobContext]("default", MyOverallContext{Name: "overall"})
	want.AddJob(MyJobContext{Count: 0, Name: "job-0"})
	
	// Get the job ID (now a UUID)
	var jobID string
	for id := range want.Jobs {
		jobID = id
		break
	}
	
	j := want.Jobs[jobID]
	j.StateErrors = map[string][]string{"key": {"e1", "e2"}}
	want.UpdateJob(j)

	cp := filepath.Join(tempDir, "state.json")
	ws, run, err := NewJsonSerializer[MyOverallContext, MyJobContext](cp, JsonSerializerConfig{})
	require.NoError(t, err)
	defer ws.Close()

	run.UpdateJob(want.Jobs[jobID])
	require.NoError(t, ws.CheckpointSync())

	actualRun, err := ws.Deserialize()
	require.NoError(t, err)

	clearLast := func(r *Run[MyOverallContext, MyJobContext]) {
		for k := range r.Jobs {
			j := r.Jobs[k]
			j.LastUpdate = nil
			r.Jobs[k] = j
		}
	}
	clearLast(want)
	clearLast(actualRun)
	assert.True(t, want.Equal(actualRun))
}
