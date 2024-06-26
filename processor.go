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

// Job represents the current processing state of any job
type Job[JC any] struct {
	Id          string              // Id is a unique identifier for the job
	C           JC                  // C holds the job specific context
	State       string              // State represents the current processing state of the job
	StateErrors map[string][]string // StateErrors is a map of errors that occurred in the current state
}

// Run is basically the overall state of a given run (batch) in the processing framework
// it's meant to be re-entrant, eg if you kill the processor and you have a serializaer, you can
// restart using it at any time
type Run[OC any, JC any] struct {
	Name    string             // Name of the run
	Jobs    map[string]Job[JC] // Map of jobs, where keys are job ids and values are Job states
	Overall OC                 // Overall overall state that is usful to all jobs, basically context for the overall batch
}

// NewRun creates a new Run instance with the given name and overall context
//
// Use the overall context to store any state that all of the jobs will want access to instead of
// storing it in the specific JobContexts
func NewRun[OC any, JC any](name string, oc OC) *Run[OC, JC] {
	return &Run[OC, JC]{
		Name:    name,
		Jobs:    map[string]Job[JC]{},
		Overall: oc,
	}
}

// Add a job to the pool, this shouldn't be called once it's running
func (r *Run[OC, JC]) AddJob(jc JC) {
	// TODO: Use a uuid for the jobs
	id := fmt.Sprintf("%d", len(r.Jobs))
	j := Job[JC]{
		Id:          id,
		C:           jc,
		State:       TRIGGER_STATE_NEW,
		StateErrors: map[string][]string{},
	}
	slog.Info("AddJob", "run", r.Name, "job", j, "totalJobs", len(r.Jobs))
	r.Jobs[id] = j
}

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
	State string
	Count int
}

// Processor executes a job
type Processor[AC any, OC any, JC any] struct {
	appContext     AC
	states         []State[AC, OC, JC]
	serializer     Serializer[OC, JC]
	statusListener StatusListener
	initted        bool
	stateMap       map[string]State[AC, OC, JC]
	stateNames     []string
	stateChan      map[string]chan Job[JC]
	returnChan     chan Return[JC]
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

	// When a job changes state, we send it to this channel to centrally manage and re-queue
	p.returnChan = make(chan Return[JC], 10_000)

	// For each state, we need a channel of jobs
	p.stateChan = map[string]chan Job[JC]{}

	// Create the state chans
	for _, s := range p.states {
		if s.Terminal {
			continue
		}
		p.stateChan[s.TriggerState] = make(chan Job[JC], 10_000)
	}
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

	// wgReturn := sync.WaitGroup{}

	// Now we gotta kick off all of the states to their correct queue
	go func() { p.enqueueAllJobs(r) }()

	// Make a central processor and start it
	wg.Add(1)
	go func() {
		for rtn := range p.returnChan {
			j := rtn.Job

			// Send the new kicks if any
			p.kickJobs(rtn, j, r)

			// Update the job
			r.Jobs[j.Id] = j

			// Flush the state
			err := p.serializer.Serialize(*r)
			if err != nil {
				log.Fatalf("Error serializing, aborting now to not lose work: %v", err)
			}

			// Calculate state counts
			statusCountMap := map[string]int{}
			for _, j := range r.Jobs {
				statusCountMap[j.State]++
			}
			statusCount := make([]StatusCount, 0, len(statusCountMap))
			for _, state := range p.states {
				statusCount = append(statusCount, StatusCount{
					State: state.TriggerState,
					Count: statusCountMap[state.TriggerState],
				})
			}
			p.statusListener.StatusUpdate(statusCount)

			// Sent the job to the next state channel
			nextState, ok := p.stateMap[j.State]
			if !ok {
				stateNames := make([]string, 0, len(p.stateMap))
				log.Fatalf("No state [%s] found in the state map, valid states, %s", j.State, strings.Join(stateNames, ", "))
			}
			// If it's terminal, we're done with this job
			if !nextState.Terminal {
				if rtn.Error != nil {
					j.StateErrors[j.State] = append(j.StateErrors[j.State], rtn.Error.Error())
					// send it back to the state
					p.sendJob(j)
					continue
				}
				// We need to get the chan for the next one
				nextChan := p.stateChan[nextState.TriggerState]
				// Send the job to the next chan
				nextChan <- j
				continue
			}
			// If the state was terminal, we should see if all of the states are terminated, if so shut down
			if !p.allJobsAreTerminal(r) {
				continue
			}

			p.shutdown()

			break
		}
		wg.Done()
	}()

	// Wait for all of the processors to quit
	wg.Wait()

	return nil
}

func (p *Processor[AC, OC, JC]) allJobsAreTerminal(r *Run[OC, JC]) bool {
	allTerminal := true
	for _, j := range r.Jobs {
		// TODO: Refactor this to be a method on Job that takes the state map
		if !p.stateMap[j.State].Terminal {
			slog.Info("Job not terminal",
				"job", j.Id,
				"state", j.State,
			)
			allTerminal = false
			break
		}
	}
	return allTerminal
}

func (p *Processor[AC, OC, JC]) enqueueAllJobs(r *Run[OC, JC]) {
	slog.Info("Enqueing Jobs", "jobCount", len(r.Jobs))
	for _, job := range r.Jobs {
		// If it's in a terminal state, skip
		if p.stateMap[job.State].Terminal {
			continue
		}
		slog.Info("Enqueing Job", "state", job.State, "job", job.Id)
		p.sendJob(job)
	}
	slog.Info("All Jobs Enqueue", "jobCount", len(r.Jobs))
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
	if rtn.KickRequests != nil {
		for _, k := range rtn.KickRequests {
			// Add the new job to the state
			newJob := Job[JC]{
				Id:          fmt.Sprintf("%s->%d", j.Id, len(r.Jobs)),
				C:           k.C,
				State:       k.State,
				StateErrors: map[string][]string{},
			}
			// Add it to r
			r.Jobs[newJob.Id] = newJob

			nextState, ok := p.stateMap[newJob.State]
			if !ok {
				log.Fatal(p.invalidStateError(newJob.State))
			}
			// If it's terminal, we're done with this job
			if !nextState.Terminal {
				// We need to get the chan for the next one
				nextChan := p.stateChan[nextState.TriggerState]
				// Send the job to the next chan
				nextChan <- newJob
				continue
			}
		}
	}
}

func (p *Processor[AC, OC, JC]) sendJob(job Job[JC]) {
	go func() { p.stateChan[job.State] <- job }()
}

func (p *Processor[AC, OC, JC]) execFunc(ctx context.Context, r *Run[OC, JC], s State[AC, OC, JC], i int, wg sync.WaitGroup) func() {
	wg.Add(1)
	return func() {
		slog.Info("Starting worker", "worker", i, "state", s.TriggerState)
		c := p.stateChan[s.TriggerState]
		for j := range c {
			if s.RateLimit != nil {
				s.RateLimit.Wait(ctx)
			}
			// Execute the job
			rtn := Return[JC]{}
			slog.Info("Executing job", "job", j.Id, "state", s.TriggerState)
			j.C, j.State, rtn.KickRequests, rtn.Error = s.Exec(ctx, p.appContext, r.Overall, j.C)
			slog.Info("Execution complete", "job", j.Id, "state", s.TriggerState, "newState", j.State, "error", rtn.Error, "kickRequests", len(rtn.KickRequests))

			rtn.Job = j
			p.returnChan <- rtn
		}
		wg.Done()
		slog.Info("Stopped worker", "worker", i, "state", s.TriggerState)
	}
}

func (p *Processor[AC, OC, JC]) invalidStateError(s string) error {
	return fmt.Errorf("State [%s] has no executor, valid state names: %s", s, strings.Join(p.stateNames, ", "))
}
