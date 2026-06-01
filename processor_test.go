package jorb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

// import "github.com/stretchr/testify/assert"

const (
	STATE_DONE     = "done"
	STATE_MIDDLE   = "middle"
	STATE_DONE_TWO = "done_two"
)

func createJob(state string) Job[MyJobContext] {
	return Job[MyJobContext]{
		Id:    "",
		C:     MyJobContext{},
		State: state,
	}
}

func TestStateStorage(t *testing.T) {
	concurrency := 5
	stateS := newStateStorageFromStates([]State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				return jc, STATE_DONE, nil, nil
			},
			Terminal:    false,
			Concurrency: concurrency,
		},
		{
			TriggerState: STATE_DONE,
			Terminal:     true,
		},
	})

	// Fake processor that just takes jobs and throws them away, as the StateStorage doesn't actually care about
	// Any of the actual processing
	go func() {
		for true {
			select {
			case <-stateS.stateChan[TRIGGER_STATE_NEW]:
				continue
			}
		}
	}()

	for i := 0; i < concurrency*2; i++ {
		stateS.processJob(createJob(TRIGGER_STATE_NEW))
	}
	assert.Equal(t, []StatusCount{
		{
			State:    STATE_DONE,
			Terminal: true,
		},
		{
			State:     TRIGGER_STATE_NEW,
			Executing: concurrency,
			Waiting:   concurrency,
		},
	}, stateS.getStatusCounts())
	for i := 0; i < 2; i++ {
		stateS.runNextWaitingJob(TRIGGER_STATE_NEW)
		stateS.processJob(createJob(STATE_DONE))
	}

	assert.Equal(t, []StatusCount{
		{
			State:     STATE_DONE,
			Terminal:  true,
			Completed: 2,
		},
		{
			State:     TRIGGER_STATE_NEW,
			Executing: concurrency,
			Waiting:   concurrency - 2,
		},
	}, stateS.getStatusCounts())

	for i := 0; i < concurrency-2; i++ {
		stateS.runNextWaitingJob(TRIGGER_STATE_NEW)
		stateS.processJob(createJob(STATE_DONE))
	}

	assert.Equal(t, []StatusCount{
		{
			State:     STATE_DONE,
			Terminal:  true,
			Completed: concurrency,
		},
		{
			State:     TRIGGER_STATE_NEW,
			Executing: concurrency,
			Waiting:   0,
		},
	}, stateS.getStatusCounts())

	for i := 0; i < concurrency; i++ {
		stateS.runNextWaitingJob(TRIGGER_STATE_NEW)
		stateS.processJob(createJob(STATE_DONE))
	}

	assert.Equal(t, []StatusCount{
		{
			State:     STATE_DONE,
			Terminal:  true,
			Completed: concurrency * 2,
		},
		{
			State:     TRIGGER_STATE_NEW,
			Executing: 0,
			Waiting:   0,
		},
	}, stateS.getStatusCounts())
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
	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count += 1
				time.Sleep(time.Second)
				return jc, STATE_DONE, nil, nil
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

	p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)
	assert.NoError(t, err)

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
	for i := 0; i < 5; i++ {
		r.AddJobWithState(MyJobContext{
			Count: 0,
		}, STATE_DONE_TWO)
	}
	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: STATE_DONE_TWO,
			Terminal:     true,
		},
		{
			TriggerState: TRIGGER_STATE_NEW,
			Terminal:     true,
		},
	}

	testSl := testStatusListener{
		t: t,
		expectedStatuses: [][]StatusCount{
			{
				StatusCount{
					State:     STATE_DONE_TWO,
					Completed: 5,
					Terminal:  true,
				},
				StatusCount{
					State:     TRIGGER_STATE_NEW,
					Completed: 10,
					Terminal:  true,
				},
			},
		},
	}

	p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, &testSl)
	assert.NoError(t, err)

	start := time.Now()
	err = p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*2, "Should take less than 2 seconds when run in parallel")
	// Should have gotten an update
	assert.Equal(t, 1, testSl.cur)
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
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count += 1
				return jc, STATE_MIDDLE, nil, nil
			},
			Terminal:    false,
			Concurrency: 10,
		},
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_MIDDLE,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count += 1
				return jc, STATE_DONE, nil, nil
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

	p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)
	assert.NoError(t, err)

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

	prev := log.Writer()
	log.SetOutput(io.Discard)
	defer func() {
		log.SetOutput(prev)
	}()
	//t.Parallel()
	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 40; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}
	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		State[MyAppContext, MyOverallContext, MyJobContext]{
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
			Concurrency: 10,
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

	p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)
	assert.NoError(t, err)

	start := time.Now()
	err = p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*10, "Should take less than 10 seconds when run in parallel")

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
	for i := 0; i < 11; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}

	tl := &testStatusListener{
		t: t,
	}
	tl.ExpectStatus([]StatusCount{
		{
			State:     TRIGGER_STATE_NEW,
			Waiting:   1,
			Executing: 10,
			Completed: 0,
		},
		{
			State:     STATE_DONE,
			Waiting:   0,
			Executing: 0,
			Completed: 0,
			Terminal:  true,
		},
	})
	for i := 0; i <= 10; i++ {
		tl.ExpectStatus([]StatusCount{
			{
				State:     TRIGGER_STATE_NEW,
				Waiting:   0,
				Executing: 10 - i,
				Completed: 0,
			},
			{
				State:     STATE_DONE,
				Waiting:   0,
				Executing: 0,
				Completed: 1 + i,
				Terminal:  true,
			},
		})
	}

	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		State[MyAppContext, MyOverallContext, MyJobContext]{
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
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}

	p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, tl)
	assert.NoError(t, err)

	start := time.Now()
	err = p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*2, "Should take less than 2 seconds when run in parallel")

	for _, j := range r.Jobs {
		assert.Equal(t, 1, j.C.Count, "Job Count should be 1")
	}
}

func TestFairness(t *testing.T) {
	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 5; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}
	totalCount := 0
	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				totalCount++
				if totalCount > 10 {
					return jc, STATE_DONE, nil, nil
				}

				jc.Count++

				return jc, TRIGGER_STATE_NEW, nil, nil
			},
			Concurrency: 1,
		},
		{
			TriggerState: STATE_DONE,
			Terminal:     true,
		},
	}

	p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)
	assert.NoError(t, err)

	err = p.Exec(context.Background(), r)
	for _, job := range r.Jobs {
		assert.Equal(t, 2, job.C.Count)
	}
}

func TestClose(t *testing.T) {
	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	r.AddJob(MyJobContext{
		Count: 0,
	})

	for _, testCase := range []struct {
		testName    string
		states      []State[MyAppContext, MyOverallContext, MyJobContext]
		shouldError bool
	}{
		{
			testName: "keeps running",
			states: []State[MyAppContext, MyOverallContext, MyJobContext]{
				{
					TriggerState: TRIGGER_STATE_NEW,
					Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
						time.Sleep(50 * time.Millisecond)
						return jc, TRIGGER_STATE_NEW, nil, nil
					},
					Concurrency: 1,
				},
			},
			shouldError: true,
		},
		{
			testName: "completes",
			states: []State[MyAppContext, MyOverallContext, MyJobContext]{
				{
					TriggerState: TRIGGER_STATE_NEW,
					Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
						time.Sleep(50 * time.Millisecond)
						return jc, "done", nil, nil
					},
					Concurrency: 1,
				},
				{
					TriggerState: "done",
					Terminal:     true,
				},
			},
			shouldError: false,
		},
	} {
		t.Run(testCase.testName, func(t *testing.T) {
			p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, testCase.states, nil, nil)

			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				time.Sleep(100 * time.Millisecond)
				cancel()
			}()

			err = p.Exec(ctx, r)
			if testCase.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestStatusCountDedup(t *testing.T) {
	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	r.AddJob(MyJobContext{
		Count: 0,
	})
	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count++
				if jc.Count < 10 {
					return jc, TRIGGER_STATE_NEW, nil, nil
				}
				return jc, STATE_DONE, nil, nil
			},
			Concurrency: 1,
		},
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_DONE,
			Terminal:     true,
		},
	}

	testSl := testStatusListener{
		t: t,
		expectedStatuses: [][]StatusCount{
			{
				StatusCount{
					State:     STATE_DONE,
					Completed: 0,
					Terminal:  true,
				},
				StatusCount{
					State:     TRIGGER_STATE_NEW,
					Executing: 1,
				},
			},
			{
				StatusCount{
					State:     STATE_DONE,
					Completed: 1,
					Terminal:  true,
				},
				StatusCount{
					State: TRIGGER_STATE_NEW,
				},
			},
		},
	}

	p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, &testSl)
	assert.NoError(t, err)

	err = p.Exec(context.Background(), r)
	time.Sleep(1 * time.Second)
	assert.Equal(t, 2, testSl.cur)
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
	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		State[MyAppContext, MyOverallContext, MyJobContext]{
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
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}

	p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)
	assert.NoError(t, err)

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
	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		State[MyAppContext, MyOverallContext, MyJobContext]{
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
		State[MyAppContext, MyOverallContext, MyJobContext]{
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
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}

	p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)
	assert.NoError(t, err)

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
	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count += 1
				return jc, STATE_DONE, nil, nil
			},
			Terminal:    false,
			Concurrency: concurrency,                                                        // When we have multiple workers we might have multiple limiters
			RateLimit:   rate.NewLimiter(rate.Every(time.Second*time.Duration(seconds)), 1), // Every 5 seconds
		},
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}

	p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)
	assert.NoError(t, err)

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
	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count += 1
				return jc, STATE_MIDDLE, nil, nil
			},
			Terminal:    false,
			Concurrency: 10,
		},
		State[MyAppContext, MyOverallContext, MyJobContext]{
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
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}

	p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)
	assert.NoError(t, err)

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

// TestProcessor_JsonSerializer_RestartPreservesRun exercises the real persistence path:
// Processor completion → persistAfterCompletion → Serializer.JobUpdate on the live run,
// then process exit without an explicit CheckpointSync so restart must recover via checkpoint + JSONL replay.
func TestProcessor_JsonSerializer_RestartPreservesRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "run.json")

	ws, r, err := NewJsonSerializer[MyOverallContext, MyJobContext](statePath)
	require.NoError(t, err)

	// Must use the Run returned by NewJsonSerializer — it is the same pointer the store snapshots.
	r.AddJob(MyJobContext{Count: 0})

	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				kicks := []KickRequest[MyJobContext]{
					{C: MyJobContext{Name: "fan-a"}, State: STATE_MIDDLE},
					{C: MyJobContext{Name: "fan-b"}, State: STATE_MIDDLE},
				}
				jc.Count = 7
				return jc, STATE_DONE, kicks, nil
			},
			Concurrency: 1,
		},
		{
			TriggerState: STATE_MIDDLE,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count += 10
				return jc, STATE_DONE, nil, nil
			},
			Concurrency: 2,
		},
		{
			TriggerState: STATE_DONE,
			Terminal:     true,
		},
	}

	ac := MyAppContext{}
	p, err := NewProcessor(ac, states, ws, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = p.Exec(ctx, r)
	require.NoError(t, err)

	require.Len(t, r.Jobs, 3)
	for _, j := range r.Jobs {
		require.Equal(t, STATE_DONE, j.State)
	}
	
	// Find the original job and kicked jobs by checking for "->" in the ID
	var originalJob Job[MyJobContext]
	var kickJobs []Job[MyJobContext]
	for _, j := range r.Jobs {
		if !strings.Contains(j.Id, "->") {
			originalJob = j
		} else {
			kickJobs = append(kickJobs, j)
		}
	}
	require.Equal(t, 7, originalJob.C.Count)
	require.Len(t, kickJobs, 2)
	for _, k := range kickJobs {
		require.Equal(t, 10, k.C.Count)
	}

	mem := r
	require.NoError(t, ws.Close())

	ws2, r2, err := NewJsonSerializer[MyOverallContext, MyJobContext](statePath)
	require.NoError(t, err)
	defer ws2.Close()

	require.True(t, mem.Equal(r2), "reopened run must match post-exec memory state (checkpoint + JSONL replay)")
}

func TestProcessor_Serialization(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	ws, r, err := NewJsonSerializer[MyOverallContext, MyJobContext](statePath)
	require.NoError(t, err)

	ac := MyAppContext{}
	for i := 0; i < 10; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}
	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				if jc.Count == 1 {
					return jc, STATE_DONE, nil, errors.New("errored again")
				}

				jc.Count += 1
				time.Sleep(time.Second)
				return jc, TRIGGER_STATE_NEW, nil, errors.New("errored")
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

	p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, ws, nil)
	assert.NoError(t, err)

	start := time.Now()
	err = p.Exec(context.Background(), r)
	delta := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, delta, time.Second*4, "Should take less than 4 seconds when run in parallel")

	for _, j := range r.Jobs {
		assert.Equal(t, 1, j.C.Count, "Job Count should be 1")
		assert.Equal(t, map[string][]string{TRIGGER_STATE_NEW: {"errored", "errored again"}}, j.StateErrors)
	}

	require.NoError(t, ws.Close())

	ws2, actual, err := NewJsonSerializer[MyOverallContext, MyJobContext](statePath)
	require.NoError(t, err)
	defer ws2.Close()

	assert.Equal(t, len(r.Jobs), len(actual.Jobs))
	assert.True(t, r.Equal(actual))
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
	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		// This state generates a list of job requests
		State[MyAppContext, MyOverallContext, MyJobContext]{
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
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_MIDDLE,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				jc.Count = len(jc.String)
				return jc, STATE_DONE_TWO, nil, nil
			},
			Terminal:    false,
			Concurrency: 10,
		},
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_DONE_TWO,
			Exec:         nil,
			Terminal:     true,
		},
	}

	p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states, nil, nil)
	assert.NoError(t, err)

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

func TestProcessor_AIMDBackoff(t *testing.T) {
	type AC struct{}
	type OC struct{}
	type JC struct {
		attemptCount int
	}

	rateLimitCount := 0
	successCount := 0

	states := []State[AC, OC, JC]{
		{
			TriggerState: "new",
			Exec: func(ctx context.Context, ac AC, oc OC, jc JC) (JC, string, []KickRequest[JC], error) {
				jc.attemptCount++
				// First 3 attempts hit rate limit, then succeed
				if jc.attemptCount <= 3 {
					rateLimitCount++
					return jc, "new", nil, &RateLimitError{Err: fmt.Errorf("rate limited")}
				}
				successCount++
				return jc, "done", nil, nil
			},
			Concurrency: 1,
			RateLimit:   NewAIMDRateLimiter(100, 10, 200),
		},
		{
			TriggerState: "done",
			Terminal:     true,
		},
	}

	p, err := NewProcessor(AC{}, states, &NilSerializer[OC, JC]{}, &NilStatusListener{})
	if err != nil {
		t.Fatalf("Failed to create processor: %v", err)
	}

	r := NewRun[OC, JC]("test-run", OC{})
	r.AddJob(JC{})

	err = p.Exec(context.Background(), r)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}

	if rateLimitCount != 3 {
		t.Errorf("Expected 3 rate limit errors, got %d", rateLimitCount)
	}
	if successCount != 1 {
		t.Errorf("Expected 1 success, got %d", successCount)
	}

	// Verify job completed
	require.Len(t, r.Jobs, 1)
	var job Job[JC]
	for _, j := range r.Jobs {
		job = j
		break
	}
	if job.State != "done" {
		t.Errorf("Expected job to be in 'done' state, got '%s'", job.State)
	}

	// Verify errors were logged
	if len(job.StateErrors["new"]) != 3 {
		t.Errorf("Expected 3 errors logged for 'new' state, got %d", len(job.StateErrors["new"]))
	}
}

func TestProcessor_AIMDWithMultipleJobs(t *testing.T) {
	type AC struct{}
	type OC struct{}
	type JC struct {
		id           string
		shouldFail   bool
		attemptCount int
	}

	aimdLimiter := NewAIMDRateLimiter(50, 10, 100)
	initialRate := aimdLimiter.Current()

	states := []State[AC, OC, JC]{
		{
			TriggerState: "process",
			Exec: func(ctx context.Context, ac AC, oc OC, jc JC) (JC, string, []KickRequest[JC], error) {
				jc.attemptCount++
				if jc.shouldFail && jc.attemptCount == 1 {
					return jc, "process", nil, &RateLimitError{Err: fmt.Errorf("rate limited")}
				}
				return jc, "done", nil, nil
			},
			Concurrency: 5,
			RateLimit:   aimdLimiter,
		},
		{
			TriggerState: "done",
			Terminal:     true,
		},
	}

	p, err := NewProcessor(AC{}, states, &NilSerializer[OC, JC]{}, &NilStatusListener{})
	if err != nil {
		t.Fatalf("Failed to create processor: %v", err)
	}

	r := NewRun[OC, JC]("test-run", OC{})
	// Add jobs: some will fail once, others succeed immediately
	for i := 0; i < 10; i++ {
		r.AddJobWithState(JC{id: fmt.Sprintf("job-%d", i), shouldFail: i%3 == 0}, "process")
	}

	err = p.Exec(context.Background(), r)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}

	// Verify all jobs completed
	for _, job := range r.Jobs {
		if job.State != "done" {
			t.Errorf("Job %s not in 'done' state: %s", job.Id, job.State)
		}
	}

	// Rate should have changed due to backoffs and successes
	finalRate := aimdLimiter.Current()
	if finalRate == initialRate {
		t.Errorf("Expected rate to change from initial %f, but it stayed the same", initialRate)
	}

	// Rate should be within bounds
	if finalRate < 10 || finalRate > 100 {
		t.Errorf("Final rate %f outside bounds [10, 100]", finalRate)
	}
}

func TestProcessor_StandardRateLimiterStillWorks(t *testing.T) {
	type AC struct{}
	type OC struct{}
	type JC struct{}

	states := []State[AC, OC, JC]{
		{
			TriggerState: "new",
			Exec: func(ctx context.Context, ac AC, oc OC, jc JC) (JC, string, []KickRequest[JC], error) {
				return jc, "done", nil, nil
			},
			Concurrency: 1,
			RateLimit:   rate.NewLimiter(10, 10),
		},
		{
			TriggerState: "done",
			Terminal:     true,
		},
	}

	p, err := NewProcessor(AC{}, states, &NilSerializer[OC, JC]{}, &NilStatusListener{})
	if err != nil {
		t.Fatalf("Failed to create processor: %v", err)
	}

	r := NewRun[OC, JC]("test-run", OC{})
	r.AddJob(JC{})

	err = p.Exec(context.Background(), r)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}

	require.Len(t, r.Jobs, 1)
	var job Job[JC]
	for _, j := range r.Jobs {
		job = j
		break
	}
	if job.State != "done" {
		t.Errorf("Expected job to be in 'done' state, got '%s'", job.State)
	}
}

func TestProcessor_HighThroughputNoOpJobs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")

	ws, r, err := NewJsonSerializer[MyOverallContext, MyJobContext](statePath)
	require.NoError(t, err)
	defer ws.Close()

	const jobCount = 500
	for i := 0; i < jobCount; i++ {
		r.AddJob(MyJobContext{Count: i})
	}

	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				return jc, STATE_MIDDLE, nil, nil
			},
			Concurrency: 150,
		},
		{
			TriggerState: STATE_MIDDLE,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				return jc, STATE_DONE, nil, nil
			},
			Concurrency: 150,
		},
		{
			TriggerState: STATE_DONE,
			Terminal:     true,
		},
	}

	p, err := NewProcessor(MyAppContext{}, states, ws, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	err = p.Exec(ctx, r)
	elapsed := time.Since(start)
	require.NoError(t, err)

	for _, j := range r.Jobs {
		require.Equal(t, STATE_DONE, j.State)
	}
	require.Less(t, elapsed, 30*time.Second, "no-op jobs took too long: %v", elapsed)
	t.Logf("processed %d jobs through 3 states in %v", jobCount, elapsed)
}

func TestProcessor_CheckpointNotTriggeredDuringProcessing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")

	ws, r, err := NewJsonSerializer[MyOverallContext, MyJobContext](statePath)
	require.NoError(t, err)

	var gate sync.Mutex
	gate.Lock()

	const jobCount = 50
	for i := 0; i < jobCount; i++ {
		r.AddJob(MyJobContext{Count: i})
	}

	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
				gate.Lock()
				gate.Unlock()
				return jc, STATE_DONE, nil, nil
			},
			Concurrency: jobCount,
		},
		{
			TriggerState: STATE_DONE,
			Terminal:     true,
		},
	}

	p, err := NewProcessor(MyAppContext{}, states, ws, nil)
	require.NoError(t, err)

	// Record checkpoint mtime after init (materializeCleanIncremental writes one)
	initStat, err := os.Stat(statePath)
	require.NoError(t, err)
	initMtime := initStat.ModTime()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- p.Exec(ctx, r)
	}()

	// Wait for all jobs to be executing (blocked on gate)
	time.Sleep(100 * time.Millisecond)

	// Checkpoint mtime should not have changed — timer is 1 hour, no kick from JobUpdate
	afterStat, err := os.Stat(statePath)
	require.NoError(t, err)
	require.Equal(t, initMtime, afterStat.ModTime(), "checkpoint should not have been rewritten while jobs are in-flight with long CheckpointInterval")

	// Release all jobs
	gate.Unlock()

	err = <-done
	require.NoError(t, err)

	for _, j := range r.Jobs {
		require.Equal(t, STATE_DONE, j.State)
	}

	// Incremental file should exist before Close (jobs were appended)
	incrementalPath := filepath.Join(dir, "state.incremental.jsonl")
	_, err = os.Stat(incrementalPath)
	require.NoError(t, err, "incremental JSONL should exist before Close")

	// Close should write final checkpoint and clean up incremental files
	require.NoError(t, ws.Close())

	// Checkpoint should have been updated
	finalStat, err := os.Stat(statePath)
	require.NoError(t, err)
	require.True(t, finalStat.ModTime().After(initMtime), "Close must write a final checkpoint")

	// Sealed segments should be cleaned up
	matches, _ := filepath.Glob(filepath.Join(dir, "state.incremental.sealed.*.jsonl"))
	require.Empty(t, matches, "sealed segments should be removed after Close")

	// Active incremental file may exist but should be empty (re-created by checkpoint)
	info, err := os.Stat(incrementalPath)
	if err == nil {
		require.Equal(t, int64(0), info.Size(), "incremental JSONL should be empty after Close")
	}
}

func TestProcessor_NoRateLimiterStillWorks(t *testing.T) {
	type AC struct{}
	type OC struct{}
	type JC struct{}

	states := []State[AC, OC, JC]{
		{
			TriggerState: "new",
			Exec: func(ctx context.Context, ac AC, oc OC, jc JC) (JC, string, []KickRequest[JC], error) {
				return jc, "done", nil, nil
			},
			Concurrency: 1,
			RateLimit:   nil,
		},
		{
			TriggerState: "done",
			Terminal:     true,
		},
	}

	p, err := NewProcessor(AC{}, states, &NilSerializer[OC, JC]{}, &NilStatusListener{})
	if err != nil {
		t.Fatalf("Failed to create processor: %v", err)
	}

	r := NewRun[OC, JC]("test-run", OC{})
	r.AddJob(JC{})

	err = p.Exec(context.Background(), r)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}

	require.Len(t, r.Jobs, 1)
	var job Job[JC]
	for _, j := range r.Jobs {
		job = j
		break
	}
	if job.State != "done" {
		t.Errorf("Expected job to be in 'done' state, got '%s'", job.State)
	}
}

// --- Timeout tests ---

// timeoutTestStates returns a minimal three-state set used by the timeout tests:
// a starting state that runs exec, a configurable fail state, and a done state.
func timeoutTestStates(exec func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error), timeout *Timeout) []State[MyAppContext, MyOverallContext, MyJobContext] {
	return []State[MyAppContext, MyOverallContext, MyJobContext]{
		{
			TriggerState: TRIGGER_STATE_NEW,
			Exec:         exec,
			Concurrency:  1,
			Timeout:      timeout,
		},
		{
			TriggerState: STATE_DONE,
			Terminal:     true,
		},
		{
			TriggerState: STATE_DONE_TWO,
			Terminal:     true,
		},
	}
}

// TestTimeout_FiresAndRoutesToFailState verifies that an Exec that exceeds the
// configured Timeout.Duration is interrupted, the job lands in FailState, and
// a "timed out after" entry is recorded in StateErrors. The driver respects
// ctx so the deadline returns promptly rather than running the full sleep.
func TestTimeout_FiresAndRoutesToFailState(t *testing.T) {
	t.Parallel()

	exec := func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
		select {
		case <-time.After(2 * time.Second):
			return jc, STATE_DONE, nil, nil
		case <-ctx.Done():
			return jc, "", nil, ctx.Err()
		}
	}
	states := timeoutTestStates(exec, &Timeout{
		Duration:  50 * time.Millisecond,
		FailState: STATE_DONE_TWO,
	})

	r := NewRun[MyOverallContext, MyJobContext]("timeout-fires", MyOverallContext{})
	r.AddJob(MyJobContext{})

	p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](MyAppContext{}, states, nil, nil)
	require.NoError(t, err)

	start := time.Now()
	require.NoError(t, p.Exec(context.Background(), r))
	elapsed := time.Since(start)

	require.Len(t, r.Jobs, 1)
	var job Job[MyJobContext]
	for _, j := range r.Jobs {
		job = j
	}
	assert.Equal(t, STATE_DONE_TWO, job.State, "should land in configured FailState, not the success target")
	require.Len(t, job.StateErrors[TRIGGER_STATE_NEW], 1)
	assert.Contains(t, job.StateErrors[TRIGGER_STATE_NEW][0], "timed out after 50ms")
	assert.Less(t, elapsed, time.Second, "should fail well before the 2s sleep completes")
}

// TestTimeout_NotExceededRunsToCompletion verifies that an Exec finishing
// before the deadline still completes normally and is not misclassified.
func TestTimeout_NotExceededRunsToCompletion(t *testing.T) {
	t.Parallel()

	exec := func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
		return jc, STATE_DONE, nil, nil
	}
	states := timeoutTestStates(exec, &Timeout{
		Duration:  5 * time.Second,
		FailState: STATE_DONE_TWO,
	})

	r := NewRun[MyOverallContext, MyJobContext]("timeout-no-fire", MyOverallContext{})
	r.AddJob(MyJobContext{})

	p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](MyAppContext{}, states, nil, nil)
	require.NoError(t, err)
	require.NoError(t, p.Exec(context.Background(), r))

	for _, j := range r.Jobs {
		assert.Equal(t, STATE_DONE, j.State)
		assert.Empty(t, j.StateErrors[TRIGGER_STATE_NEW])
	}
}

// TestTimeout_ParentCancelDoesNotMasquerade verifies that cancelling the outer
// context returned from p.Exec does not surface as a "timed out" classification.
// The deadline disambiguation in the worker requires the parent ctx to still be
// healthy at the moment DeadlineExceeded is observed; otherwise the original
// error must propagate.
func TestTimeout_ParentCancelDoesNotMasquerade(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	exec := func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
		close(started)
		select {
		case <-time.After(5 * time.Second):
			return jc, STATE_DONE, nil, nil
		case <-ctx.Done():
			return jc, "", nil, ctx.Err()
		}
	}
	// A long Timeout so the parent cancel races first.
	states := timeoutTestStates(exec, &Timeout{
		Duration:  10 * time.Second,
		FailState: STATE_DONE_TWO,
	})

	r := NewRun[MyOverallContext, MyJobContext]("timeout-parent-cancel", MyOverallContext{})
	r.AddJob(MyJobContext{})

	parentCtx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()

	p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](MyAppContext{}, states, nil, nil)
	require.NoError(t, err)
	// Exec returns the parent ctx error when cancelled mid-run.
	execErr := p.Exec(parentCtx, r)
	assert.ErrorIs(t, execErr, context.Canceled)

	// The error logged on the job (if any) must be the original ctx.Canceled,
	// not a "timed out" reclassification. The worker may also exit before
	// updating the run, in which case the job stays in TRIGGER_STATE_NEW.
	for _, j := range r.Jobs {
		for _, msg := range j.StateErrors[TRIGGER_STATE_NEW] {
			assert.NotContains(t, msg, "timed out after",
				"parent cancellation must not surface as a timeout: %q", msg)
		}
	}
}

// TestTimeout_RetryGetsFreshWindow verifies that a state which re-enqueues
// itself receives a fresh deadline on each attempt. A job that fails three
// half-deadline attempts and succeeds on the fourth must complete cleanly:
// each attempt observes the full Timeout.Duration, not a shared one.
func TestTimeout_RetryGetsFreshWindow(t *testing.T) {
	t.Parallel()

	const timeout = 200 * time.Millisecond
	const halfTimeout = timeout / 2

	var mu sync.Mutex
	attempts := 0
	exec := func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		// First three attempts sleep half the deadline (well within budget) and
		// re-enqueue with an error; the fourth completes.
		select {
		case <-time.After(halfTimeout):
		case <-ctx.Done():
			return jc, "", nil, ctx.Err()
		}
		if n < 4 {
			return jc, TRIGGER_STATE_NEW, nil, fmt.Errorf("retry %d", n)
		}
		return jc, STATE_DONE, nil, nil
	}
	states := timeoutTestStates(exec, &Timeout{
		Duration:  timeout,
		FailState: STATE_DONE_TWO,
	})

	r := NewRun[MyOverallContext, MyJobContext]("timeout-fresh-window", MyOverallContext{})
	r.AddJob(MyJobContext{})

	p, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](MyAppContext{}, states, nil, nil)
	require.NoError(t, err)
	require.NoError(t, p.Exec(context.Background(), r))

	for _, j := range r.Jobs {
		assert.Equal(t, STATE_DONE, j.State, "fresh deadline per attempt should let retries succeed")
	}
	mu.Lock()
	assert.Equal(t, 4, attempts)
	mu.Unlock()
}

// TestTimeout_ValidationRejectsBadConfig confirms that NewProcessor refuses to
// construct a state machine with a malformed Timeout: zero/negative duration,
// missing FailState, FailState referencing an unregistered state, or Timeout
// declared on a terminal state.
func TestTimeout_ValidationRejectsBadConfig(t *testing.T) {
	noopExec := func(ctx context.Context, ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, []KickRequest[MyJobContext], error) {
		return jc, STATE_DONE, nil, nil
	}

	cases := []struct {
		name   string
		states []State[MyAppContext, MyOverallContext, MyJobContext]
		want   string
	}{
		{
			name: "zero duration",
			states: []State[MyAppContext, MyOverallContext, MyJobContext]{
				{TriggerState: TRIGGER_STATE_NEW, Exec: noopExec, Concurrency: 1, Timeout: &Timeout{Duration: 0, FailState: STATE_DONE}},
				{TriggerState: STATE_DONE, Terminal: true},
			},
			want: "Timeout.Duration must be positive",
		},
		{
			name: "negative duration",
			states: []State[MyAppContext, MyOverallContext, MyJobContext]{
				{TriggerState: TRIGGER_STATE_NEW, Exec: noopExec, Concurrency: 1, Timeout: &Timeout{Duration: -time.Second, FailState: STATE_DONE}},
				{TriggerState: STATE_DONE, Terminal: true},
			},
			want: "Timeout.Duration must be positive",
		},
		{
			name: "missing fail state",
			states: []State[MyAppContext, MyOverallContext, MyJobContext]{
				{TriggerState: TRIGGER_STATE_NEW, Exec: noopExec, Concurrency: 1, Timeout: &Timeout{Duration: time.Second, FailState: ""}},
				{TriggerState: STATE_DONE, Terminal: true},
			},
			want: "Timeout requires a FailState",
		},
		{
			name: "fail state not registered",
			states: []State[MyAppContext, MyOverallContext, MyJobContext]{
				{TriggerState: TRIGGER_STATE_NEW, Exec: noopExec, Concurrency: 1, Timeout: &Timeout{Duration: time.Second, FailState: "ghost"}},
				{TriggerState: STATE_DONE, Terminal: true},
			},
			want: `FailState "ghost" is not a registered state`,
		},
		{
			name: "timeout on terminal state",
			states: []State[MyAppContext, MyOverallContext, MyJobContext]{
				{TriggerState: TRIGGER_STATE_NEW, Exec: noopExec, Concurrency: 1},
				{TriggerState: STATE_DONE, Terminal: true, Timeout: &Timeout{Duration: time.Second, FailState: TRIGGER_STATE_NEW}},
			},
			want: "terminal state done cannot define a Timeout",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](MyAppContext{}, tc.states, nil, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
