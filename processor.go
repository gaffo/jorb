package jorb

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"runtime/pprof"
	"sort"
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
}

// KickRequest struct is a job context with a requested state that the
// framework will expand into an actual job
type KickRequest[JC any] struct {
	C     JC
	State string
}

type StatusCount struct {
	State     string
	Completed int
	Executing int
	Waiting   int
	Terminal  bool
}

type state struct {
}

type stateThing[AC any, OC any, JC any] struct {
	// This is fine for package-internal use cases to directly iterate over
	states []State[AC, OC, JC]

	// These shouldn't be used outside stateThing's methods
	stateMap            map[string]State[AC, OC, JC]
	stateStatusMap      map[string]*StatusCount
	stateWaitingJobsMap map[string][]Job[JC]
	stateChan           map[string]chan Job[JC]
	sortedStateNames    []string
}

func newStateThingFromStates[AC any, OC any, JC any](states []State[AC, OC, JC]) stateThing[AC, OC, JC] {
	st := stateThing[AC, OC, JC]{
		states:              states,
		stateMap:            map[string]State[AC, OC, JC]{},
		stateStatusMap:      map[string]*StatusCount{},
		stateWaitingJobsMap: map[string][]Job[JC]{},
		stateChan:           map[string]chan Job[JC]{},
		sortedStateNames:    []string{},
	}

	for _, s := range states {
		stateName := s.TriggerState

		st.sortedStateNames = append(st.sortedStateNames, stateName)
		st.stateMap[stateName] = s
		st.stateStatusMap[stateName] = &StatusCount{
			State:    stateName,
			Terminal: s.Terminal,
		}
		// This is by-design unbuffered
		st.stateChan[stateName] = make(chan Job[JC])
	}

	sort.Strings(st.sortedStateNames)

	return st
}

func (s stateThing[AC, OC, JC]) getJobChannelForState(stateName string) chan Job[JC] {
	return s.stateChan[stateName]
}

func (s stateThing[AC, OC, JC]) validate() error {
	for _, state := range s.states {
		if state.Terminal {
			if state.Concurrency < 0 {
				return fmt.Errorf("terminal state %s has negative concurrency", state.TriggerState)
			}
		} else {
			if state.Concurrency < 1 {
				return fmt.Errorf("non-terminal state %s has non-positive concurrency", state.TriggerState)
			}
			if state.Exec == nil {
				return fmt.Errorf("non-terminal state %s but has no Exec function", state.TriggerState)
			}
		}
	}

	return nil
}

func (s stateThing[AC, OC, JC]) runJob(job Job[JC]) {
	s.stateStatusMap[job.State].Executing += 1
	s.stateChan[job.State] <- job
}

func (s stateThing[AC, OC, JC]) queueJob(job Job[JC]) {
	s.stateStatusMap[job.State].Waiting += 1
	s.stateWaitingJobsMap[job.State] = append(s.stateWaitingJobsMap[job.State], job)
}

func (s stateThing[AC, OC, JC]) completeJob(job Job[JC]) {
	s.stateStatusMap[job.State].Completed += 1
}

func (s stateThing[AC, OC, JC]) processJob(job Job[JC]) {
	if s.isTerminal(job) {
		s.completeJob(job)
	} else {
		if s.canRunJobForState(job.State) {
			s.runJob(job)
		} else {
			s.queueJob(job)
		}
	}
}

func (s stateThing[AC, OC, JC]) isTerminal(job Job[JC]) bool {
	return s.stateMap[job.State].Terminal
}

func (s stateThing[AC, OC, JC]) allJobsAreTerminal(r *Run[OC, JC]) bool {
	for _, job := range r.Jobs {
		if !s.isTerminal(job) {
			return false
		}
	}
	return true
}

func (s stateThing[AC, OC, JC]) runNextWaitingJob(state string) {
	// One less job is executing for the prior state
	s.stateStatusMap[state].Executing -= 1

	// There are no waiting jobs for the state, so we have nothing to queue
	waitingJobCount := len(s.stateWaitingJobsMap[state])
	if waitingJobCount == 0 {
		return
	}

	job := s.stateWaitingJobsMap[state][waitingJobCount-1]
	s.stateWaitingJobsMap[state] = s.stateWaitingJobsMap[state][0 : waitingJobCount-1]
	s.stateStatusMap[job.State].Waiting -= 1

	s.runJob(job)
}

func (s stateThing[AC, OC, JC]) canRunJobForState(state string) bool {
	return s.stateStatusMap[state].Executing < s.stateMap[state].Concurrency
}

func (s stateThing[AC, OC, JC]) hasExecutingJobs() bool {
	for _, value := range s.stateStatusMap {
		if value.Executing > 0 {
			return true
		}
	}

	return false
}

func (s stateThing[AC, OC, JC]) getStatusCounts() []StatusCount {
	ret := make([]StatusCount, 0)
	for _, name := range s.sortedStateNames {
		ret = append(ret, *s.stateStatusMap[name])
	}
	return ret
}

// Serializer is an interface that defines how to serialize and deserialize job contexts.

// Processor executes a job
type Processor[AC any, OC any, JC any] struct {
	appContext     AC
	serializer     Serializer[OC, JC]
	stateThing     stateThing[AC, OC, JC]
	statusListener StatusListener
	returnChan     chan Return[JC]
	wg             sync.WaitGroup
}

// Return is a struct that contains a job and a list of kick requests
// that is used for returning job updates to the system
type Return[JC any] struct {
	PriorState   string
	Job          Job[JC]
	KickRequests []KickRequest[JC]
	Error        error
}

func NewProcessor[AC any, OC any, JC any](ac AC, states []State[AC, OC, JC], serializer Serializer[OC, JC], statusListener StatusListener) *Processor[AC, OC, JC] {
	return &Processor[AC, OC, JC]{
		appContext:     ac,
		stateThing:     newStateThingFromStates(states),
		serializer:     serializer,
		statusListener: statusListener,
	}
}

func (p *Processor[AC, OC, JC]) init() {
	if p.serializer == nil {
		p.serializer = &NilSerializer[OC, JC]{}
	}
	if p.statusListener == nil {
		p.statusListener = &NilStatusListener{}
	}

	// This is by-design unbuffered
	p.returnChan = make(chan Return[JC])
}

// Exec this big work function, this does all the crunching
func (p *Processor[AC, OC, JC]) Exec(ctx context.Context, r *Run[OC, JC]) error {
	if err := p.stateThing.validate(); err != nil {
		return err
	}

	p.init()

	if p.stateThing.allJobsAreTerminal(r) {
		slog.Info("AllJobsTerminal")
		return nil
	}

	// create the workers
	for _, s := range p.stateThing.states {
		// Terminal states don't need to recieve jobs, they're just done
		if s.Terminal {
			continue
		}

		p.execFunc(ctx, s, r.Overall, &p.wg)
	}

	pprof.Do(ctx, pprof.Labels("type", "main"), func(ctx context.Context) {
		p.wg.Add(1)
		go p.process(ctx, r, &p.wg)
	})

	p.wg.Wait()
	return nil
}

func (p *Processor[AC, OC, JC]) process(ctx context.Context, r *Run[OC, JC], wg *sync.WaitGroup) {
	defer func() {
		p.shutdown()
		wg.Done()
	}()

	// Enqueue the jobs to start
	for _, job := range r.Jobs {
		p.stateThing.processJob(job)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case completedJob := <-p.returnChan:
			// If the prior state of the completed job was at capacity, we now have space for one more
			p.stateThing.runNextWaitingJob(completedJob.PriorState)

			// Update the run with the new state
			r.UpdateJob(completedJob.Job)
			p.stateThing.processJob(completedJob.Job)

			// Start any of the new jobs that need kicking
			for idx, kickRequest := range completedJob.KickRequests {
				job := Job[JC]{
					Id:          fmt.Sprintf("%s->%d", completedJob.Job.Id, idx),
					C:           kickRequest.C,
					State:       kickRequest.State,
					StateErrors: map[string][]string{},
				}
				r.UpdateJob(job)
				p.stateThing.processJob(job)
			}

			if err := p.serializer.Serialize(*r); err != nil {
				log.Fatalf("Error serializing, aborting now to not lose work: %v", err)
			}

			p.updateStatus()

			if p.stateThing.allJobsAreTerminal(r) && !p.stateThing.hasExecutingJobs() {
				return
			}
		}
	}
}

func (p *Processor[AC, OC, JC]) updateStatus() {
	p.statusListener.StatusUpdate(p.stateThing.getStatusCounts())
}

func (p *Processor[AC, OC, JC]) shutdown() {
	// close all of the channels
	for _, state := range p.stateThing.states {
		close(p.stateThing.getJobChannelForState(state.TriggerState))
	}
	// close ourselves down
	close(p.returnChan)
}

type StateExec[AC any, OC any, JC any] struct {
	ctx        context.Context
	ac         AC
	oc         OC
	state      State[AC, OC, JC]
	jobChan    <-chan Job[JC]
	returnChan chan<- Return[JC]
	i          int
	wg         *sync.WaitGroup
}

func (s *StateExec[AC, OC, JC]) Run() {
	slog.Info("Starting worker", "worker", s.i, "state", s.state.TriggerState)
	defer func() {
		s.wg.Done()
		slog.Info("Stopped worker", "worker", s.i, "state", s.state.TriggerState)
	}()

	for {
		select {
		case <-s.ctx.Done():
			return
		case j, more := <-s.jobChan:
			// The channel was closed
			if !more {
				return
			}

			if s.state.RateLimit != nil {
				s.state.RateLimit.Wait(s.ctx)
				slog.Info("LimiterAllowed", "worker", s.i, "state", s.state.TriggerState, "job", j.Id)
			}
			// Execute the job
			rtn := Return[JC]{
				PriorState: j.State,
			}
			slog.Info("Executing job", "job", j.Id, "state", s.state.TriggerState)
			j.C, j.State, rtn.KickRequests, rtn.Error = s.state.Exec(s.ctx, s.ac, s.oc, j.C)
			slog.Info("Execution complete", "job", j.Id, "state", s.state.TriggerState, "newState", j.State, "error", rtn.Error, "kickRequests", len(rtn.KickRequests))

			rtn.Job = j
			slog.Info("Returning job", "job", j.Id, "newState", j.State)
			s.returnChan <- rtn
			slog.Info("Returned job", "job", j.Id, "newState", j.State)
		}
	}
}

func (p *Processor[AC, OC, JC]) execFunc(ctx context.Context, state State[AC, OC, JC], overallContext OC, wg *sync.WaitGroup) {
	// Make workers for each, they just process and fire back to the central channel
	for i := 0; i < state.Concurrency; i++ {
		p.wg.Add(1)
		stateExec := StateExec[AC, OC, JC]{
			ctx:        ctx,
			ac:         p.appContext,
			oc:         overallContext,
			state:      state,
			jobChan:    p.stateThing.getJobChannelForState(state.TriggerState),
			returnChan: p.returnChan,
			i:          i,
			wg:         wg,
		}

		pprof.Do(ctx, pprof.Labels("type", "worker", "state", state.TriggerState, "id", fmt.Sprintf("%d", i)), func(ctx context.Context) {
			go stateExec.Run()
		})
	}
}
