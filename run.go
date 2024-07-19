package jorb

import (
	"fmt"
	"log/slog"
	"sync"
)

// Run is basically the overall state of a given run (batch) in the processing framework
// it's meant to be re-entrant, eg if you kill the processor and you have a serializaer, you can
// restart using it at any time
type Run[OC any, JC any] struct {
	Name    string             // Name of the run
	Jobs    map[string]Job[JC] // Map of jobs, where keys are job ids and values are Job states
	Overall OC                 // Overall overall state that is usful to all jobs, basically context for the overall batch
	m       sync.Mutex         // Mutex used for indexing operations
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

	for _, j := range r.Jobs {
		// if it doesn't have a last event, give it one
		if j.LastUpdate == nil {
			j.UpdateLastEvent()
		}
	}
}

func (r *Run[OC, JC]) UpdateJob(j Job[JC]) {
	r.m.Lock()
	defer r.m.Unlock()

	r.Jobs[j.Id] = j.UpdateLastEvent()
}

func (r *Run[OC, JC]) AddJobWithState(jc JC, state string) {
	r.m.Lock()
	defer r.m.Unlock()

	// TODO: Use a uuid for the jobs
	id := fmt.Sprintf("%d", len(r.Jobs))
	j := Job[JC]{
		Id:          id,
		C:           jc,
		State:       state,
		StateErrors: map[string][]string{},
	}

	slog.Info("AddJob", "run", r.Name, "job", j, "totalJobs", len(r.Jobs))
	r.Jobs[id] = j.UpdateLastEvent()
}

// Add a job to the pool, this shouldn't be called once it's running
func (r *Run[OC, JC]) AddJob(jc JC) {
	r.AddJobWithState(jc, TRIGGER_STATE_NEW)
}
