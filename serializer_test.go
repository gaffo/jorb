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
	assert.EqualValues(t, *run, actualRun)
}
