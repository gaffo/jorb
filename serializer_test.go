package jorb

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestJsonSerializer_SaveLoad(t *testing.T) {
	t.Parallel()
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "test")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test run
	run := NewRun[MyOverallContext, MyJobContext]("test", MyOverallContext{Name: "overall"})
	// Add 10 jobs with random data
	for i := 0; i < 10; i++ {
		job := MyJobContext{Count: 0, Name: fmt.Sprintf("job-%d", i)}
		run.AddJob(job)
	}

	// Create a JsonSerializer with a temporary file
	tempFile := filepath.Join(tempDir, "test.json")
	serializer := &JsonSerializer[MyOverallContext, MyJobContext]{File: tempFile}

	// Serialize the run
	err = serializer.Serialize(*run)
	require.NoError(t, err)

	require.FileExists(t, tempFile)

	actualRun, err := serializer.Deserialize()
	require.NoError(t, err)

	// Check that the run is the same
	assert.EqualValues(t, run, actualRun)
}

func Test_SerializeWithError(t *testing.T) {
	t.Parallel()
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "test")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	r := NewRun[MyOverallContext, MyJobContext]("test", MyOverallContext{Name: "overall"})
	r.Return(Job[MyJobContext]{
		C: MyJobContext{Count: 0, Name: "job-0"},
		StateErrors: map[string][]string{
			"key": []string{
				"e1", "e2",
			},
		},
	})

	tempFile := filepath.Join(tempDir, "test.json")
	serializer := &JsonSerializer[MyOverallContext, MyJobContext]{File: tempFile}

	err = serializer.Serialize(*r)
	require.NoError(t, err)

	actualRun, err := serializer.Deserialize()
	require.NoError(t, err)

	// Nill out the last updates so we don't have to do assert near
	for k, _ := range r.Jobs {
		j := r.Jobs[k]
		j.LastUpdate = nil
		r.Jobs[k] = j

		j = actualRun.Jobs[k]
		j.LastUpdate = nil
		actualRun.Jobs[k] = j
	}
	assert.EqualValues(t, r, actualRun)
}
