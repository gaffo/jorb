package jorb

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_AddJobWithState(t *testing.T) {
	t.Parallel()
	r := NewRun[MyOverallContext, MyJobContext]("job", MyOverallContext{})
	r.AddJobWithState(MyJobContext{Count: 0}, "other_state")
	assert.Equal(t, 1, len(r.Jobs))
	
	// Get the job ID (now a UUID)
	var jobID string
	var originalTime *time.Time
	for id, j := range r.Jobs {
		jobID = id
		assert.Equal(t, "other_state", j.State)
		originalTime = j.LastUpdate
		break
	}
	
	time.Sleep(1 * time.Second)

	r.UpdateJob(Job[MyJobContext]{
		Id: jobID,
		C: MyJobContext{
			Count: 1,
		},
		State: "other_state_2",
	})

	time.Sleep(1 * time.Second)
	// Number of jobs has not changed
	assert.Equal(t, 1, len(r.Jobs))
	// Job's state has been updated
	assert.Equal(t, "other_state_2", r.Jobs[jobID].State)
	// Job's time has been updated
	assert.NotEqual(t, originalTime, r.Jobs[jobID].LastUpdate)
}
