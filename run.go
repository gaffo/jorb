package jorb

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

// Run is basically the overall state of a given run (batch) in the processing framework
// it's meant to be re-entrant, eg if you kill the processor and you have a serializaer, you can
// restart using it at any time
type Run[OC any, JC any] struct {
	Name                 string             // Name of the run
	Jobs                 map[string]Job[JC] // Map of jobs, where keys are job ids and values are Job states
	Overall              OC                 // Overall overall state that is usful to all jobs, basically context for the overall batch
	m                    sync.Mutex         // Mutex used for indexing operations
	lockedJobsById       map[string]bool    // map of job id to bool to see if this job is currently in flight, emphemeral to an active processing session
	lockedJobsStateCount map[string]int
	stateCount           map[string]int
}

// NewRun creates a new Run instance with the given name and overall context
//
// Use the overall context to store any state that all of the jobs will want access to instead of
// storing it in the specific JobContexts
func NewRun[OC any, JC any](name string, oc OC) *Run[OC, JC] {
	return &Run[OC, JC]{
		Name:                 name,
		Jobs:                 map[string]Job[JC]{},
		Overall:              oc,
		m:                    sync.Mutex{},
		lockedJobsById:       map[string]bool{},
		lockedJobsStateCount: map[string]int{},
		stateCount:           map[string]int{},
	}
}

func (r *Run[OC, JC]) Init() {
	r.m.Lock()
	defer r.m.Unlock()
	r.lockedJobsById = map[string]bool{}
	r.lockedJobsStateCount = map[string]int{}
	r.stateCount = map[string]int{}
	r.updateStateCounts()
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
	slog.Info("AddJob", "run", r.Name, "job", j, "totalJobs", len(r.Jobs))
	r.Jobs[id] = j
	r.updateStateCounts()
}

func (r *Run[OC, JC]) NextJobForState(state string) (Job[JC], bool) {
	r.m.Lock()
	defer r.m.Unlock()
	stateJobs := []Job[JC]{}

	for _, j := range r.Jobs {
		if j.State == state {
			if r.lockedJobsById[j.Id] {
				continue
			}
			stateJobs = append(stateJobs, j)
		}
	}

	if len(stateJobs) == 0 {
		return Job[JC]{}, false
	}

	// Sort the jobs by LastEvent ascending
	sort.Slice(stateJobs, func(i, j int) bool {
		if stateJobs[i].LastUpdate == nil {
			return true
		}
		if stateJobs[j].LastUpdate == nil {
			return false
		}
		return stateJobs[i].LastUpdate.Before(*stateJobs[j].LastUpdate)
	})

	// Mark the job as locked
	ret := stateJobs[0]
	// lock it
	r.lockedJobsById[ret.Id] = true
	r.updateStateCounts()

	// update it's last event
	r.Jobs[ret.Id] = ret.UpdateLastEvent()

	// return it
	return r.Jobs[ret.Id], true
}

func (r *Run[OC, JC]) Return(j Job[JC]) {
	r.m.Lock()
	defer r.m.Unlock()

	r.Jobs[j.Id] = j.UpdateLastEvent()
	delete(r.lockedJobsById, j.Id)

	r.updateStateCounts()
}

func (r *Run[OC, JC]) updateStateCounts() {
	r.lockedJobsStateCount = map[string]int{}
	for id, v := range r.lockedJobsById {
		if !v {
			continue
		}
		j := r.Jobs[id]
		r.lockedJobsStateCount[j.State] += 1
	}
	r.stateCount = map[string]int{}
	for _, j := range r.Jobs {
		r.stateCount[j.State] += 1
	}
}

func (r *Run[OC, JC]) JobsInFlight() bool {
	r.m.Lock()
	defer r.m.Unlock()
	// if any of the jobs are in flight, return true
	for _, j := range r.Jobs {
		if r.lockedJobsById[j.Id] {
			return true
		}
	}
	return false
}

func (r *Run[OC, JC]) StatusCounts() map[string]StatusCount {
	ret := map[string]StatusCount{}

	for k, v := range r.stateCount {
		ret[k] = StatusCount{
			State:     k,
			Count:     v,
			Executing: r.lockedJobsStateCount[k],
		}
	}

	return ret
}
