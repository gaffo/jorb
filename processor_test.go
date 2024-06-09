package jorb

import (
	"log"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// import "github.com/stretchr/testify/assert"

// JobContext represents my Job's context, eg the state of doing work
type MyJobContext struct {
	Count int
}

// MyOverallContext any non-job specific state that is important for the overall run
type MyOverallContext struct {
}

// MyAppContext is all of my application processing, clients, etc reference for the job processors
type MyAppContext struct {
}

const (
	STATE_DONE = "done"
)

func TestProcessorOneJob(t *testing.T) {
	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 10; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}

	newFunc := func(ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, error) {
		log.Println("Processing New")
		jc.Count += 1
		time.Sleep(time.Second)
		return jc, STATE_DONE, nil
	}

	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: TRIGGER_STATE_NEW,
			Exec:         &newFunc,
			Terminal:     false,
			Concurrency:  10,
		},
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}

	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states)

	start := time.Now()
	err := p.Exec(r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*2, "Should take less than 2 seconds when run in parallel")

	for _, j := range r.Jobs {
		if j.C.Count != 1 {
			assert.Equal(t, 1, j.C.Count, "Job Count should be 1")
			t.Errorf("Expected Job %s Count to be 1 but was %d", j.Id, j.C.Count)
		}
	}
}
