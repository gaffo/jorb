package jorb

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

// import "github.com/stretchr/testify/assert"

// JobContext represents my Job's context, eg the state of doing work
type MyJobContext struct {
	Name       string
	Count      int
	StringList []string
	String     string
}

// MyOverallContext any non-job specific state that is important for the overall run
type MyOverallContext struct {
	Name string
}

// MyAppContext is all of my application processing, clients, etc reference for the job processors
type MyAppContext struct {
}

const (
	STATE_DONE     = "done"
	STATE_MIDDLE   = "middle"
	STATE_DONE_TWO = "done_two"
)

func TestMain(m *testing.M) {
	prev := log.Writer()
	log.SetOutput(io.Discard)
	m.Run()
	log.SetOutput(prev)
}

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
	states, err := NewStates([]State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count += 1
				time.Sleep(time.Second)
				return jc, STATE_DONE, nil, nil
			},
			Terminal:    false,
			Concurrency: 10,
		},
		{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}, false)
	require.NoError(t, err)

	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)

	start := time.Now()
	err = p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*2, "Should take less than 2 seconds when run in parallel")

	for _, j := range r.Jobs {
		assert.Equal(t, 1, j.C.Count, "Job Count should be 1")
	}
}

func TestProcessorAllTerminal(t *testing.T) {
	t.Parallel()
	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 10; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}
	states, err := NewStates([]State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Terminal:     true,
		},
	}, false)
	require.NoError(t, err)

	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)

	start := time.Now()
	err = p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*2, "Should take less than 2 seconds when run in parallel")
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
	states, err := NewStates([]State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count += 1
				return jc, STATE_MIDDLE, nil, nil
			},
			Terminal:    false,
			Concurrency: 10,
		},
		{
			TriggerState: STATE_MIDDLE,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count += 1
				return jc, STATE_DONE, nil, nil
			},
			Terminal:    false,
			Concurrency: 10,
		},
		{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}, false)
	require.NoError(t, err)

	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)

	start := time.Now()
	err = p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*2, "Should take less than 2 seconds when run in parallel")

	for _, j := range r.Jobs {
		assert.Equal(t, 2, j.C.Count, "Job Count should be 1")
	}
}

func TestProcessor_TwoTerminal(t *testing.T) {
	f, err := os.Create("cpu.pprof")
	require.NoError(t, err)
	defer f.Close()

	m, err := os.Create("heap.pprof")
	require.NoError(t, err)
	defer m.Close()

	err = pprof.StartCPUProfile(f)
	require.NoError(t, err)
	defer pprof.StopCPUProfile()

	defer func() {
		err = pprof.WriteHeapProfile(m)
		require.NoError(t, err)
	}()
	//t.Parallel()
	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 30_000; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}
	states, err := NewStates([]State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				time.Sleep(time.Millisecond * time.Duration(rand.Intn(1000)))
				jc.Count += 1
				c := rand.Intn(2) == 0
				if c {
					return jc, STATE_DONE, nil, nil
				}
				return jc, STATE_DONE_TWO, nil, nil
			},
			Terminal:    false,
			Concurrency: 1000,
		},
		{
			TriggerState: STATE_DONE_TWO,
			Exec:         nil,
			Terminal:     true,
		},
		{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}, false)
	require.NoError(t, err)

	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)

	start := time.Now()
	err = p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*16, "Should take less than 9 seconds when run in parallel")

	stateCount := map[string]int{}
	for _, j := range r.Jobs {
		assert.Equal(t, 1, j.C.Count, "Job Count should be 1")
		stateCount[j.State] += 1
	}
	assert.GreaterOrEqual(t, stateCount[STATE_DONE_TWO], len(r.Jobs)/3)
	assert.GreaterOrEqual(t, stateCount[STATE_DONE], len(r.Jobs)/3)
	log.Printf("Total Time: %v\n", delta)
}

type testStatusListener struct {
	t                *testing.T
	cur              int
	expectedStatuses [][]StatusCount
}

func (t *testStatusListener) StatusUpdate(status []StatusCount) {
	t.t.Helper()
	if t.cur >= len(t.expectedStatuses) {
		t.t.Errorf("Unexpected status update: %v", status)
		return
	}
	expected := t.expectedStatuses[t.cur]
	require.Equal(t.t, expected, status)
	t.cur++
}

func (t *testStatusListener) ExpectStatus(counts []StatusCount) {
	t.expectedStatuses = append(t.expectedStatuses, counts)
}

var _ StatusListener = &testStatusListener{}

func TestProcessor_StateCallback(t *testing.T) {
	t.Skip("Need to do a better job of the assert state machine")
	prev := log.Writer()
	log.SetOutput(io.Discard)
	defer func() {
		log.SetOutput(prev)
	}()

	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 1; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}

	tl := &testStatusListener{
		t: t,
	}
	tl.ExpectStatus([]StatusCount{
		{
			State: TRIGGER_STATE_NEW,
			Count: 1,
		},
		{
			State:    STATE_DONE,
			Count:    0,
			Terminal: true,
		},
	})
	tl.ExpectStatus([]StatusCount{
		{
			State:     TRIGGER_STATE_NEW,
			Count:     1,
			Executing: 1,
		},
		{
			State:    STATE_DONE,
			Count:    0,
			Terminal: true,
		},
	})
	tl.ExpectStatus([]StatusCount{
		{
			State:     TRIGGER_STATE_NEW,
			Count:     1,
			Executing: 1,
		},
		{
			State:    STATE_DONE,
			Count:    0,
			Terminal: true,
		},
	})
	tl.ExpectStatus([]StatusCount{
		{
			State: TRIGGER_STATE_NEW,
			Count: 0,
		},
		{
			State:    STATE_DONE,
			Count:    1,
			Terminal: true,
		},
	})

	states, err := NewStates([]State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				//log.Println("Processing New")
				jc.Count += 1
				time.Sleep(time.Second)
				return jc, STATE_DONE, nil, nil
			},
			Terminal:    false,
			Concurrency: 10,
		},
		{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}, false)
	require.NoError(t, err)

	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, tl)

	start := time.Now()
	err = p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*2, "Should take less than 2 seconds when run in parallel")

	for _, j := range r.Jobs {
		assert.Equal(t, 1, j.C.Count, "Job Count should be 1")
	}
}

func TestProcessor_Retries(t *testing.T) {
	t.Parallel()
	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 10; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}
	states, err := NewStates([]State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count++
				if jc.Count <= 3 {
					return jc, TRIGGER_STATE_NEW, nil, fmt.Errorf("New error")
				}
				return jc, STATE_DONE, nil, nil
			},
			Terminal:    false,
			Concurrency: 10,
		},
		{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}, false)
	require.NoError(t, err)

	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)

	start := time.Now()
	err = p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*2, "Should take less than 2 seconds when run in parallel")

	for _, j := range r.Jobs {
		assert.Equal(t, 4, j.C.Count)
	}
}

func TestProcessor_StateLog(t *testing.T) {
	t.Parallel()
	t.Skip()
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
	states, err := NewStates([]State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count += 1
				time.Sleep(time.Second)
				return jc, STATE_MIDDLE, nil, nil
			},
			Terminal:    false,
			Concurrency: 10,
			RateLimit:   rate.NewLimiter(10, 1),
		},
		{
			TriggerState: STATE_MIDDLE,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count += 1
				time.Sleep(time.Second)
				return jc, STATE_DONE, nil, nil
			},
			Terminal:    false,
			Concurrency: 10,
			RateLimit:   rate.NewLimiter(10, 1),
		},
		{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}, false)
	require.NoError(t, err)

	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)

	start := time.Now()
	err = p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*4)

	for _, j := range r.Jobs {
		assert.Equal(t, 2, j.C.Count, "Job Count should be 1")
	}
}

func TestProcessor_RateLimiterSlows(t *testing.T) {
	t.Parallel()
	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 3; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}
	concurrency := 2
	seconds := 1.0
	states, err := NewStates([]State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count += 1
				return jc, STATE_DONE, nil, nil
			},
			Terminal:    false,
			Concurrency: concurrency,                                                        // When we have multiple workers we might have multiple limiters
			RateLimit:   rate.NewLimiter(rate.Every(time.Second*time.Duration(seconds)), 1), // Every 5 seconds
		},
		{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}, false)
	require.NoError(t, err)

	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)

	start := time.Now()
	err = p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	jobCount := len(r.Jobs)
	expected := time.Second * time.Duration(float64(jobCount)/seconds-1)
	assert.Less(t, expected, delta)

	for _, j := range r.Jobs {
		assert.Equal(t, 1, j.C.Count, j.Id)
	}
}

func TestProcessor_LoopWithExit(t *testing.T) {
	t.Parallel()
	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 10; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}
	states, err := NewStates([]State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count += 1
				return jc, STATE_MIDDLE, nil, nil
			},
			Terminal:    false,
			Concurrency: 10,
		},
		{
			TriggerState: STATE_MIDDLE,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count += 1
				if jc.Count > 9 {
					return jc, STATE_DONE, nil, nil
				}
				return jc, TRIGGER_STATE_NEW, nil, nil
			},
			Terminal:    false,
			Concurrency: 10,
		},
		{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}, false)
	require.NoError(t, err)

	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)

	start := time.Now()
	err = p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*2, "Should take less than 2 seconds when run in parallel")

	for _, j := range r.Jobs {
		assert.Equal(t, 10, j.C.Count, "Job Count should be 1")
	}
}

func TestProcessor_DLQ(t *testing.T) {
	t.Parallel()
	t.Skip()
}

func TestProcessor_Serialization(t *testing.T) {
	t.Parallel()

	tempFile, err := os.CreateTemp("", "state-*.json.tmp")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	serialzer := NewJsonSerializer[MyOverallContext, MyJobContext](tempFile.Name())

	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 10; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}
	states, err := NewStates([]State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				//log.Println("Processing New")
				jc.Count += 1
				time.Sleep(time.Second)
				return jc, STATE_DONE, nil, nil
			},
			Terminal:    false,
			Concurrency: 10,
		},
		{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}, false)
	require.NoError(t, err)

	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, serialzer, nil)

	start := time.Now()
	err = p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*2, "Should take less than 2 seconds when run in parallel")

	for _, j := range r.Jobs {
		assert.Equal(t, 1, j.C.Count, "Job Count should be 1")
	}

	// Now reload the job
	actual, err := serialzer.Deserialize()
	require.NoError(t, err)
	assert.NotNil(t, r)
	assert.Equal(t, len(r.Jobs), len(actual.Jobs))
}

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randString(length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func TestProcessor_FirstStepExpands(t *testing.T) {
	t.Parallel()
	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 10; i++ {
		jobContext := MyJobContext{
			Count:      0,
			StringList: []string{},
		}
		for i := 0; i < 10; i++ {
			// Append a 30 length randomly generated string to jobContext.StringList
			jobContext.StringList = append(jobContext.StringList, randString(30))
		}
		r.AddJob(jobContext)
	}
	states, err := NewStates([]State[MyAppContext, MyOverallContext, MyJobContext]{
		// This state generates a list of job requests
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				newJobs := []KickRequest[MyJobContext]{}
				for _, state := range jc.StringList {
					newJobs = append(newJobs, KickRequest[MyJobContext]{
						C:     MyJobContext{String: state},
						State: STATE_MIDDLE,
					})
				}

				// This state will then finish itself
				return jc, STATE_DONE, newJobs, nil
			},
			Terminal:    false,
			Concurrency: 10,
		},
		{
			TriggerState: STATE_MIDDLE,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count = len(jc.String)
				return jc, STATE_DONE_TWO, nil, nil
			},
			Terminal:    false,
			Concurrency: 10,
		},
		{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
		{
			TriggerState: STATE_DONE_TWO,
			Exec:         nil,
			Terminal:     true,
		},
	}, false)
	require.NoError(t, err)

	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)

	start := time.Now()
	err = p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*2, "Should take less than 2 seconds when run in parallel")

	stateCount := map[string]int{}
	for _, j := range r.Jobs {
		stateCount[j.State] += 1
		if j.State == STATE_DONE {
			assert.Equal(t, 0, j.C.Count, "Job Count should be 1")
			continue
		}
		assert.Equal(t, 30, j.C.Count, "Job Count should be 1")
	}
	assert.Equal(t, 10, stateCount[STATE_DONE])
	assert.Equal(t, 10*10, stateCount[STATE_DONE_TWO])
}

func TestProcessor_ValidateExits_NonTerminal_NoExitStates(t *testing.T) {
	t.Parallel()
	_, err := NewStates([]State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				return jc, TRIGGER_STATE_NEW, nil, nil
			},
			Terminal: false,
		},
	}, true)
	require.ErrorContains(t, err, fmt.Sprintf("ValidateExitStates: invalid State machine, state %s is non-terminal but has no ExitStates", TRIGGER_STATE_NEW))
}

func TestProcessor_NonTerminal_NoExitFunction(t *testing.T) {
	t.Parallel()
	_, err := NewStates([]State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Terminal:     false,
		},
	}, true)
	require.ErrorContains(t, err, fmt.Sprintf("State %s is non-terminal but has no Exec function", TRIGGER_STATE_NEW))
}

func TestProcessor_ValidExitStates_ContainingSelf_CantBeOnlyState(t *testing.T) {
	t.Parallel()
	_, err := NewStates([]State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				return jc, TRIGGER_STATE_NEW, nil, nil
			},
			ExitStates: []string{TRIGGER_STATE_NEW},
			Terminal:   false,
		},
	}, true)
	require.ErrorContains(t, err, fmt.Sprintf("ValidateExitStates: invalid State machine, state %s is non-terminal but has no ExitStates other than self", TRIGGER_STATE_NEW))
}

func TestProcessor_ValidateExitStates_InvalidExit(t *testing.T) {
	t.Parallel()
	uknownState := "UNKNOWN_EXIT_STATE"
	_, err := NewStates([]State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				return jc, uknownState, nil, nil
			},
			Terminal:   false,
			ExitStates: []string{uknownState},
		},
	}, true)
	require.ErrorContains(t, err, fmt.Sprintf("invalid exit state [%s] for state %s", uknownState, TRIGGER_STATE_NEW))
}
