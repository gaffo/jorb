package jorb

import (
	"context"
	"log"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
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
	STATE_DONE     = "done"
	STATE_MIDDLE   = "middle"
	STATE_DONE_TWO = "done_two"
)

func TestProcessorOneJob(t *testing.T) {
	t.Parallel()
	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 10; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}
	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, error) {
				log.Println("Processing New")
				jc.Count += 1
				time.Sleep(time.Second)
				return jc, STATE_DONE, nil
			},
			Terminal:    false,
			Concurrency: 10,
		},
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}

	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states)

	start := time.Now()
	err := p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*2, "Should take less than 2 seconds when run in parallel")

	for _, j := range r.Jobs {
		assert.Equal(t, 1, j.C.Count, "Job Count should be 1")
	}
}

func TestProcessorTwoSequentialJobs(t *testing.T) {
	t.Parallel()
	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 10; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}
	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, error) {
				jc.Count += 1
				return jc, STATE_MIDDLE, nil
			},
			Terminal:    false,
			Concurrency: 10,
		},
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_MIDDLE,
			Exec: func(ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, error) {
				jc.Count += 1
				return jc, STATE_DONE, nil
			},
			Terminal:    false,
			Concurrency: 10,
		},
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}

	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states)

	start := time.Now()
	err := p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*2, "Should take less than 2 seconds when run in parallel")

	for _, j := range r.Jobs {
		assert.Equal(t, 2, j.C.Count, "Job Count should be 1")
	}
}

func TestProcessor_TwoTerminal(t *testing.T) {
	t.Parallel()
	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 10_000; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}
	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, error) {
				time.Sleep(time.Millisecond * time.Duration(rand.Intn(1000)))
				jc.Count += 1
				c := rand.Intn(2) == 0
				if c {
					return jc, STATE_DONE, nil
				}
				return jc, STATE_DONE_TWO, nil
			},
			Terminal:    false,
			Concurrency: 1000,
		},
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_DONE_TWO,
			Exec:         nil,
			Terminal:     true,
		},
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}

	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states)

	start := time.Now()
	err := p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*11, "Should take less than 2 seconds when run in parallel")

	stateCount := map[string]int{}
	for _, j := range r.Jobs {
		assert.Equal(t, 1, j.C.Count, "Job Count should be 1")
		stateCount[j.State] += 1
	}
	assert.GreaterOrEqual(t, stateCount[STATE_DONE_TWO], 4000)
	assert.GreaterOrEqual(t, stateCount[STATE_DONE], 4000)
}

func TestProcessor_Retries(t *testing.T) {
	t.Parallel()
	t.Fail()
}

func TestProcessor_StateLog(t *testing.T) {
	t.Parallel()
	t.Fail()
}

func TestProcessor_RateLimiter(t *testing.T) {
	t.Parallel()
	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 10; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}
	// We have 2 states, each that take a second at a time, but we can do 10 in a second kicking
	// and so we'll take about a second to kick off the first full set of new state jobs
	// and the last one will finish about 1 second in so 2 seconds total...
	// state middle also takes 1 second, and the first one will come in at around 2 seconds
	// and fire pretty much immediately, so we shoudl come in just shy of 3 seconds
	// running 10 jobs with a rate limit of every 100 milliseconds with 10 concurrent
	// actors which is a lot faster than 2 * 1 * 10 = 20 seconds
	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, error) {
				jc.Count += 1
				time.Sleep(time.Second)
				return jc, STATE_MIDDLE, nil
			},
			Terminal:    false,
			Concurrency: 10,
			RateLimit:   rate.NewLimiter(10, 1),
		},
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_MIDDLE,
			Exec: func(ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, error) {
				jc.Count += 1
				time.Sleep(time.Second)
				return jc, STATE_DONE, nil
			},
			Terminal:    false,
			Concurrency: 10,
			RateLimit:   rate.NewLimiter(10, 1),
		},
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}

	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states)

	start := time.Now()
	err := p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*3)

	for _, j := range r.Jobs {
		assert.Equal(t, 2, j.C.Count, "Job Count should be 1")
	}
}

func TestProcessor_LoopWithExit(t *testing.T) {
	t.Parallel()
	t.Fail()
}

func TestProcessor_DLQ(t *testing.T) {
	t.Parallel()
	t.Fail()
}

func TestProcessor_JsonSerialization(t *testing.T) {
	t.Parallel()
	t.Fail()
}
