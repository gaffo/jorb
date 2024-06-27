package jorb

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestJob_UpdateLastEvent_UpdatesLastFetchToNow(t *testing.T) {
	j := Job[MyJobContext]{}
	require.Nil(t, j.LastUpdate)

	j2 := j.UpdateLastEvent()
	assert.Equal(t, j.Id, j2.Id)

	now := time.Now()
	require.NotNil(t, j2.LastUpdate)
	assert.WithinDuration(t, now, *j2.LastUpdate, time.Second)
}
