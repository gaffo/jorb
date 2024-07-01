package jorb

import (
	"fmt"
	"github.com/grafana/pyroscope-go"
	"log/slog"
	"runtime"
	"sort"
	"sync"
	"time"
)

func (r *Run[OC, JC]) initPyro() {
	runtime.SetMutexProfileFraction(1)
	runtime.SetBlockProfileRate(1)
	if r.Name == "" {
		r.Name = "jorb"
	}
	pyroscope.Start(pyroscope.Config{
		ApplicationName: r.Name,
		UploadRate:      time.Second,

		// replace this with the address of pyroscope server
		ServerAddress: "http://localhost:4040",

		// you can disable logging by setting this to nil
		Logger: pyroscope.StandardLogger,

		// you can provide static tags via a map:
		Tags: map[string]string{"JORB": "JORB"},

		ProfileTypes: []pyroscope.ProfileType{
			// these profile types are enabled by default:
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,

			// these profile types are optional:
			pyroscope.ProfileGoroutines,
			pyroscope.ProfileMutexCount,
			pyroscope.ProfileMutexDuration,
			pyroscope.ProfileBlockCount,
			pyroscope.ProfileBlockDuration,
		},
	})
}

// Run is basically the overall state of a given run (batch) in the processing framework
// it's meant to be re-entrant, eg if you kill the processor and you have a serializaer, you can
// restart using it at any time
type Run[OC any, JC any] struct {
	Name                 string               // Name of the run
	Jobs                 map[string]Job[JC]   // Map of jobs, where keys are job ids and values are Job states
	Overall              OC                   // Overall overall state that is usful to all jobs, basically context for the overall batch
	m                    sync.Mutex           // Mutex used for indexing operations
	lockedJobsById       map[string]string    // If a job is locked and what state it was locked into
	lockedJobsStateCount map[string]int       // How many jobs are locked for each state (for status)
	stateCount           map[string][]Job[JC] // Job Queues for Speed
}

// NewRun creates a new Run instance with the given name and overall context
//
// Use the overall context to store any state that all of the jobs will want access to instead of
// storing it in the specific JobContexts
func NewRun[OC any, JC any](name string, oc OC) *Run[OC, JC] {
	r := &Run[OC, JC]{
		Name:    name,
		Jobs:    map[string]Job[JC]{},
		Overall: oc,
		m:       sync.Mutex{},
	}
	r.Init()
	return r
}

func (r *Run[OC, JC]) Init() {
	r.m.Lock()
	defer r.m.Unlock()
	r.initPyro()

	r.lockedJobsById = map[string]string{}
	r.lockedJobsStateCount = map[string]int{}
	r.stateCount = map[string][]Job[JC]{}

	// initialize the state counts
	for _, j := range r.Jobs {
		s := j.State
		if _, ok := r.stateCount[s]; !ok {
			r.stateCount[s] = []Job[JC]{}
		}
		// if it doesn't have a last event, give it one
		if j.LastUpdate == nil {
			j.UpdateLastEvent()
		}
		r.stateCount[s] = append(r.stateCount[s], j)
	}
	// now that they're all added we need to sort them
	for state, list := range r.stateCount {
		sort.Slice(list, func(i, j int) bool {
			if list[i].LastUpdate == nil {
				return false
			}
			if list[j].LastUpdate == nil {
				return true
			}
			return list[i].LastUpdate.Before(*list[j].LastUpdate)
		})
		r.stateCount[state] = list
	}
}

// Add a job to the pool, this shouldn't be called once it's running
func (r *Run[OC, JC]) AddJob(jc JC) {
	r.m.Lock()
	defer r.m.Unlock()

	// TODO: Use a uuid for the jobs
	id := fmt.Sprintf("%d", len(r.Jobs))
	j := Job[JC]{
		Id:          id,
		C:           jc,
		State:       TRIGGER_STATE_NEW,
		StateErrors: map[string][]string{},
	}
	j.UpdateLastEvent()
	// Pop it onto the end of the appropriate queue
	r.stateCount[j.State] = append(r.stateCount[j.State], j)

	slog.Info("AddJob", "run", r.Name, "job", j, "totalJobs", len(r.Jobs))
	r.Jobs[id] = j
}

func (r *Run[OC, JC]) NextJobForState(state string) (Job[JC], bool) {
	r.m.Lock()
	defer r.m.Unlock()

	if len(r.stateCount[state]) == 0 {
		return Job[JC]{}, false
	}

	// pop the item off the front of the queue
	minJob := r.stateCount[state][0]
	r.stateCount[state] = r.stateCount[state][1:]

	// lock it
	r.lockedJobsById[minJob.Id] = minJob.State
	r.lockedJobsStateCount[minJob.State]++

	// update it's last event
	r.Jobs[minJob.Id] = minJob.UpdateLastEvent()

	// return it
	return r.Jobs[minJob.Id], true
}

func (r *Run[OC, JC]) Return(j Job[JC]) {
	r.m.Lock()
	defer r.m.Unlock()

	r.Jobs[j.Id] = j.UpdateLastEvent()

	// decremnt the previous state
	prevState := r.lockedJobsById[j.Id]
	r.lockedJobsStateCount[prevState]--

	// unlock it
	delete(r.lockedJobsById, j.Id)

	// push it to the back of the new state
	r.stateCount[j.State] = append(r.stateCount[j.State], j)
}

func (r *Run[OC, JC]) JobsInFlight() bool {
	r.m.Lock()
	defer r.m.Unlock()
	// if any of the jobs are in flight, return true

	if len(r.lockedJobsById) > 0 {
		return true
	}
	return false
}

func (r *Run[OC, JC]) StatusCounts() map[string]StatusCount {
	ret := map[string]StatusCount{}

	for k, v := range r.stateCount {
		ret[k] = StatusCount{
			State:     k,
			Count:     len(v) + r.lockedJobsStateCount[k],
			Executing: r.lockedJobsStateCount[k],
		}
	}

	return ret
}
