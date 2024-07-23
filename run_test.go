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
	assert.Equal(t, "other_state", r.Jobs["0"].State)
	originalTime := r.Jobs["0"].LastUpdate
	time.Sleep(1 * time.Second)

	r.UpdateJob(Job[MyJobContext]{
		Id: "0",
		C: MyJobContext{
			Count: 1,
		},
		State: "other_state_2",
	})

	time.Sleep(1 * time.Second)
	// Number of jobs has not changed
	assert.Equal(t, 1, len(r.Jobs))
	// Job's state has been updated
	assert.Equal(t, "other_state_2", r.Jobs["0"].State)
	// Job's time has been updated
	assert.NotEqual(t, originalTime, r.Jobs["0"].LastUpdate)
}
