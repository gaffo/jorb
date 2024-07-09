package jorb

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"runtime/pprof"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

const (
	TRIGGER_STATE_NEW = "new"
)

// State represents a state in a state machine for job processing.
// It defines the behavior and configuration for a particular state.
type State[AC any, OC any, JC any] struct {
	// TriggerState is the string identifier for this state.
	TriggerState string

	// Exec is a function that executes the logic for jobs in this state.
	// It takes the application context (AC), overall context (OC), and job context (JC) as input,
	// and returns the updated job context (JC), the next state string,
	// a slice of kick requests ([]KickRequest[JC]) for triggering other jobs,
	// and an error (if any).
	Exec func(ctx context.Context, ac AC, oc OC, jc JC) (JC, string, []KickRequest[JC], error)

	// Terminal indicates whether this state is a terminal state,
	// meaning that no further state transitions should occur after reaching this state.
	Terminal bool

	// Concurrency specifies the maximum number of concurrent executions allowed for this state.
	Concurrency int

	// RateLimit is an optional rate limiter for controlling the execution rate of this state. Useful when calling rate limited apis.
	RateLimit *rate.Limiter

	// A list of valid exit states for use when in validation mode on the processor
	ExitStates []string
}

// KickRequest struct is a job context with a requested state that the
// framework will expand into an actual job
type KickRequest[JC any] struct {
	C     JC
	State string
}

type StatusCount struct {
	State     string
	Count     int
	Executing int
	Terminal  bool
}

// Processor executes a job
type Processor[AC any, OC any, JC any] struct {
	appContext         AC
	states             []State[AC, OC, JC]
	serializer         Serializer[OC, JC]
	statusListener     StatusListener
	initted            bool
	stateMap           map[string]State[AC, OC, JC]
	stateNames         []string
	stateChan          map[string]chan Job[JC]
	returnChan         chan Return[JC]
	ValidateExitStates bool
}

func NewProcessor[AC any, OC any, JC any](ac AC, states []State[AC, OC, JC], serializer Serializer[OC, JC], statusListener StatusListener) *Processor[AC, OC, JC] {
	return &Processor[AC, OC, JC]{
		appContext:     ac,
		states:         states,
		serializer:     serializer,
		statusListener: statusListener,
	}
}

// Return is a struct that contains a job and a list of kick requests
// that is used for returning job updates to the system
type Return[JC any] struct {
	Job          Job[JC]
	KickRequests []KickRequest[JC]
	Error        error
}

func (p *Processor[AC, OC, JC]) init() {
	if p.initted {
		return
	}
	if p.serializer == nil {
		p.serializer = &NilSerializer[OC, JC]{}
	}
	if p.statusListener == nil {
		p.statusListener = &NilStatusListener{}
	}
	// Make a map of triggers to states so we can easily reference it
	p.stateMap = map[string]State[AC, OC, JC]{}
	for _, s := range p.states {
		p.stateMap[s.TriggerState] = s
	}
	// get a list of return state names for use
	p.stateNames = make([]string, 0, len(p.stateMap))
	for k := range p.stateMap {
		p.stateNames = append(p.stateNames, k)
	}

	// For each state, we need a channel of jobs
	p.stateChan = map[string]chan Job[JC]{}

	// Create the state chans
	totalConcurrency := 0
	for _, s := range p.states {
		if s.Terminal {
			continue
		}
		p.stateChan[s.TriggerState] = make(chan Job[JC], s.Concurrency) // make a chan
		totalConcurrency += s.Concurrency
	}

	// When a job changes state, we send it to this channel to centrally manage and re-queue
	p.returnChan = make(chan Return[JC], totalConcurrency*2) // make it the size of the total amount of in flight jobs we could have so that each worker can return a task
}

// Exec this big work function, this does all the crunching
func (p *Processor[AC, OC, JC]) Exec(ctx context.Context, r *Run[OC, JC]) error {
	p.init()

	if p.allJobsAreTerminal(r) {
		slog.Info("AllJobsTerminal")
		return nil
	}

	wg := sync.WaitGroup{}

	// create the workers
	for _, s := range p.states {
		// Terminal states don't need to recieve jobs, they're just done
		if s.Terminal {
			continue
		}
		if s.Exec == nil {
			return p.invalidStateError(s.TriggerState)
		}

		concurrency := s.Concurrency

		// Make workers for each, they just process and fire back to the central channel
		for i := 0; i < concurrency; i++ { // add a waiter for every go processor, do it before forking
			labels := pprof.Labels("type", "jorbWorker", "state", s.TriggerState, "id", fmt.Sprintf("%d", i))
			pprof.Do(ctx, labels, func(_ context.Context) {
				go p.execFunc(ctx, r, s, i, wg)()
			})
		}
	}

	go func() { p.enqueueAllJobs(r) }() // fill all the outbound queues once in a seperate goroutine to prime the pump faster

	// Make a central processor and start it
	wg.Add(1)
	pprof.Do(ctx, pprof.Labels("type", "ReturnChanWorker"), func(_ context.Context) {
		go func() {
			p.returnQueue(r, &wg)
		}()
	})

	// Wait for all of the processors to quit
	wg.Wait()

	return nil
}

func (p *Processor[AC, OC, JC]) returnQueue(r *Run[OC, JC], wg *sync.WaitGroup) {
	for {
		batch := []Return[JC]{}
	READ_BATCH:
		for {
			select {
			case rtn := <-p.returnChan:
				batch = append(batch, rtn)
			default:
				break READ_BATCH
			}
		}
		// Dispense with the jobs
		for _, rtn := range batch {
			slog.Info("ReturnChan GotJobBack", "jobId", rtn.Job.Id, "state", rtn.Job.State, "kickRequests", len(rtn.KickRequests), "error", rtn.Error)
			j := rtn.Job

			// Send the new kicks if any
			p.kickJobs(rtn, j, r)

			// Append the error if needed
			if rtn.Error != nil {
				j.StateErrors[j.State] = append(j.StateErrors[j.State], rtn.Error.Error())
			}

			// return the job
			r.Return(j)
		}
		// Now do end of batch work

		// Flush the state
		err := p.serializer.Serialize(*r)
		if err != nil {
			log.Fatalf("Error serializing, aborting now to not lose work: %v", err)
		}
		// update the status counts
		p.updateStatusCounts(r)

		// flush out any new jobs we can
		p.enqueueAllJobs(r)

		// If the state was terminal, we should see if all of the states are terminated, if so shut down
		if !p.allJobsAreTerminal(r) {
			continue
		}
		// if there are any jobs in flight in the run, keep going
		if r.JobsInFlight() {
			continue
		}

		p.shutdown()

		break
	}
	slog.Info("ReturnChanWorker Quit")
	wg.Done()
}

func (p *Processor[AC, OC, JC]) updateStatusCounts(r *Run[OC, JC]) {
	counts := r.StatusCounts()

	ret := []StatusCount{}

	for _, state := range p.states {
		if _, ok := counts[state.TriggerState]; !ok {
			ret = append(ret, StatusCount{
				State:     state.TriggerState,
				Count:     0,
				Executing: 0,
				Terminal:  state.Terminal,
			})
			continue
		}
		c := counts[state.TriggerState]
		c.Terminal = state.Terminal
		ret = append(ret, c)
	}
	p.statusListener.StatusUpdate(ret)
}

func (p *Processor[AC, OC, JC]) allJobsAreTerminal(r *Run[OC, JC]) bool {
	c := r.StatusCounts()
	for _, k := range p.states {
		if k.Terminal {
			continue
		}
		if c[k.TriggerState].Count > 0 {
			return false
		}
	}
	return true
}

func (p *Processor[AC, OC, JC]) enqueueAllJobs(r *Run[OC, JC]) {
	slog.Info("Enqueing Jobs", "jobCount", len(r.Jobs))
	enqueued := 0
	for _, state := range p.states {
		enqueued += p.enqueueJobsForState(r, state) // mutates r.Jobs
	}
	slog.Info("All Queues Primed", "jobCount", len(r.Jobs), "enqueuedCount", enqueued)
}

func (p *Processor[AC, OC, JC]) enqueueJobsForState(r *Run[OC, JC], state State[AC, OC, JC]) int {
	slog.Info("Enqueueing jobs for state", "state", state.TriggerState)
	enqueued := 0
	for {
		j, ok := r.NextJobForState(state.TriggerState)
		if !ok {
			slog.Info("No more jobs for state", "state", state.TriggerState)
			return enqueued
		}
		c := p.stateChan[state.TriggerState]
		select {
		case c <- j:
			enqueued++
			slog.Info("Enqueing Job", "state", j.State, "job", j.Id)
			continue
		default:
			r.Return(j)
			return enqueued
		}
	}
	slog.Info("Enqueued jobs for state", "state", state.TriggerState, "enqueuedCount", enqueued)
	return enqueued
}

func (p *Processor[AC, OC, JC]) shutdown() {
	// close all of the channels
	for _, c := range p.stateChan {
		close(c)
	}
	// close ourselves down
	close(p.returnChan)
}

func (p *Processor[AC, OC, JC]) kickJobs(rtn Return[JC], j Job[JC], r *Run[OC, JC]) {
	if rtn.KickRequests == nil {
		return
	}
	for _, k := range rtn.KickRequests {
		// create a new job with the right state
		newJob := Job[JC]{
			Id:          fmt.Sprintf("%s->%d", j.Id, len(r.Jobs)),
			C:           k.C,
			State:       k.State,
			StateErrors: map[string][]string{},
		}

		// validate it
		_, ok := p.stateMap[newJob.State]
		if !ok {
			log.Fatal(p.invalidStateError(newJob.State))
		}

		// return it to the run, it'll get re-enqueued by the main return loop
		r.Return(newJob)
	}
}

type StateExec[AC any, OC any, JC any] struct {
	State      State[AC, OC, JC]
	i          int
	wg         sync.WaitGroup
	c          chan Job[JC]
	Overall    OC
	ctx        context.Context
	returnChan chan Return[JC]
	ac         AC
}

func (s *StateExec[AC, OC, JC]) Run() {
	slog.Info("Starting worker", "worker", s.i, "state", s.State.TriggerState)
	for j := range s.c {
		if s.State.RateLimit != nil {
			s.State.RateLimit.Wait(s.ctx)
			slog.Info("LimiterAllowed", "worker", s.i, "state", s.State.TriggerState, "job", j.Id)
		}
		// Execute the job
		rtn := Return[JC]{}
		slog.Info("Executing job", "job", j.Id, "state", s.State.TriggerState)
		j.C, j.State, rtn.KickRequests, rtn.Error = s.State.Exec(s.ctx, s.ac, s.Overall, j.C)
		slog.Info("Execution complete", "job", j.Id, "state", s.State.TriggerState, "newState", j.State, "error", rtn.Error, "kickRequests", len(rtn.KickRequests))

		rtn.Job = j
		//go func() {
		slog.Info("Returning job", "job", j.Id, "newState", j.State)
		s.returnChan <- rtn
		slog.Info("Returned job", "job", j.Id, "newState", j.State)
		//}()
	}
	s.wg.Done()
	slog.Info("Stopped worker", "worker", s.i, "state", s.State.TriggerState)
}

func (p *Processor[AC, OC, JC]) execFunc(ctx context.Context, r *Run[OC, JC], s State[AC, OC, JC], i int, wg sync.WaitGroup) func() {
	wg.Add(1)
	e := &StateExec[AC, OC, JC]{
		State:      s,
		i:          i,
		wg:         wg,
		c:          p.stateChan[s.TriggerState],
		ctx:        ctx,
		returnChan: p.returnChan,
		ac:         p.appContext,
	}
	return e.Run
}

func (p *Processor[AC, OC, JC]) invalidStateError(s string) error {
	return fmt.Errorf("State [%s] has no executor, valid state names: %s", s, strings.Join(p.stateNames, ", "))
}
