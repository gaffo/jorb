package jorb

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRun_NextForStatus_NoJobs(t *testing.T) {
	t.Parallel()
	r := NewRun[MyOverallContext, MyJobContext]("job", MyOverallContext{})

	_, ok := r.NextJobForState(TRIGGER_STATE_NEW)
	assert.False(t, ok)
}

func TestRun_GetsNextRunOnSecondCall(t *testing.T) {
	t.Parallel()
	r := NewRun[MyOverallContext, MyJobContext]("job", MyOverallContext{})
	r.AddJob(MyJobContext{Count: 0})
	r.AddJob(MyJobContext{Count: 0})

	// Given they're all even, I expect one job to come out
	j, ok := r.NextJobForState(TRIGGER_STATE_NEW)
	assert.True(t, ok)
	assert.NotEmpty(t, j.Id)

	// Now I should get the same job back on the second call
	j2, ok := r.NextJobForState(TRIGGER_STATE_NEW)
	assert.True(t, ok)
	assert.NotEqual(t, j.Id, j2.Id)
}

func TestRun_GetNextRun_Returning(t *testing.T) {
	t.Parallel()
	r := NewRun[MyOverallContext, MyJobContext]("job", MyOverallContext{})
	r.AddJob(MyJobContext{Count: 0})

	// Given they're all even, I expect one job to come out
	j1, ok := r.NextJobForState(TRIGGER_STATE_NEW)
	assert.True(t, ok)

	// Now I should get the same job back on the second call
	_, ok = r.NextJobForState(TRIGGER_STATE_NEW)
	assert.False(t, ok)

	// If I return the first job I should then get it again, but the last event should be updated
	r.Return(j1)
	returnDate := r.Jobs[j1.Id].LastUpdate
	assert.NotEqual(t, returnDate, j1.LastUpdate)

	// Verify that we get the same id back but the returnDate has been updated again
	j2, ok := r.NextJobForState(TRIGGER_STATE_NEW)
	assert.True(t, ok)
	assert.Equal(t, j1.Id, j2.Id)

	// The date should be updated on the next fetch as well
	assert.NotEqual(t, j1.LastUpdate, j2.LastUpdate)
	assert.NotEqual(t, returnDate, j2.LastUpdate)
}

func TestRun_GetNextRun_GetsInOrder(t *testing.T) {
	t.Parallel()
	r := NewRun[MyOverallContext, MyJobContext]("job", MyOverallContext{})
	// Add 2 jobs
	r.AddJob(MyJobContext{Count: 0})
	r.AddJob(MyJobContext{Count: 0})

	// Given they're all even, I expect one job to come out
	j1, ok := r.NextJobForState(TRIGGER_STATE_NEW)
	require.True(t, ok)
	j2, ok := r.NextJobForState(TRIGGER_STATE_NEW)
	require.True(t, ok)

	// Now I return them in reverse order
	r.Return(j2)
	r.Return(j1)

	// When I get them again, I should get them in order j2, then j1 because I should be getting the one that has
	// be updated the earliest

	// If I return the first job I should then get it again, but the last event should be updated
	j3, ok := r.NextJobForState(TRIGGER_STATE_NEW)
	require.True(t, ok)
	j4, ok := r.NextJobForState(TRIGGER_STATE_NEW)
	require.True(t, ok)

	// Now validate the ids
	// The date should be updated on the next fetch as well
	assert.Equal(t, j2.Id, j3.Id)
	assert.Equal(t, j1.Id, j4.Id)
}

func TestRun_JobsInFlight2(t *testing.T) {
	t.Parallel()
	r := NewRun[MyOverallContext, MyJobContext]("job", MyOverallContext{})
	r.AddJob(MyJobContext{Count: 0})

	// no jobs checked out, there should be no jobs in flight
	require.False(t, r.JobsInFlight())

	// check out a job
	j, ok := r.NextJobForState(TRIGGER_STATE_NEW)
	require.True(t, ok)
	require.True(t, r.JobsInFlight())

	r.Return(j)
	require.False(t, r.JobsInFlight())
}
