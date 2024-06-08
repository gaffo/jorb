package jorb

import (
	"fmt"
	"strings"
)

// Job represents the current processing state of any job
type Job[JC any] struct {
	Id    string
	C     JC
	State string
}

// Run run manages state of the work a processor is doing
type Run[OC any, JC any] struct {
	Name    string
	Jobs    map[string]Job[JC]
	Overall OC
}

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
	r.Jobs[id] = Job[JC]{
		Id:    id,
		C:     jc,
		State: TRIGGER_STATE_NEW,
	}
}

const (
	TRIGGER_STATE_NEW = "new"
)

type State[AC any, OC any, JC any] struct {
	TriggerState string
	Exec         *func(ac AC, oc OC, jc JC) (JC, string, error)
	Terminal     bool
}

// Processor executes a job
type Processor[AC any, OC any, JC any] struct {
	AppContext AC
	States     []State[AC, OC, JC]
}

func NewProcessor[AC any, OC any, JC any](ac AC, states []State[AC, OC, JC]) *Processor[AC, OC, JC] {
	return &Processor[AC, OC, JC]{
		AppContext: ac,
		States:     states,
	}
}

func (p *Processor[AC, OC, JC]) Exec(r *Run[OC, JC]) error {
	for {
		didProcess := false
		for id, j := range r.Jobs {
			// Find the right executor
			var exec *State[AC, OC, JC]
			validStates := []string{}
			for _, e := range p.States {
				validStates = append(validStates, e.TriggerState)
				if e.TriggerState == j.State {
					exec = &e
					break
				}
			}
			if exec == nil {
				return fmt.Errorf("Did not find a state executor for state [%s], please add it to the states of the processor. Valid states: [%s]", j.State, strings.Join(validStates, ","))
			}

			j.C, j.State, _ = (*exec.Exec)(p.AppContext, r.Overall, j.C)
			r.Jobs[id] = j
		}
		if didProcess == false {
			break
		}
	}
	return nil
}
