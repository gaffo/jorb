package jorb

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/time/rate"
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
	Exec         func(ac AC, oc OC, jc JC) (JC, string, error)
	Terminal     bool
	Concurrency  int
	RateLimit    *rate.Limiter
}

// Serializer is an interface for job run seralization
type Serializer[OC any, JC any] interface {
	Serialize(run Run[OC, JC]) error
	Deserialize() (Run[OC, JC], error)
}

// JsonSerializer is a struct that implements Serializer and stores and loads run from a file specified
// in the File field, there  is a anonymous variable type check
type JsonSerializer[OC any, JC any] struct {
	File string
}

var _ Serializer[any, any] = (*JsonSerializer[any, any])(nil)

func (js *JsonSerializer[OC, JC]) Serialize(run Run[OC, JC]) error {
	// Create the parent directory if it doesn't exist
	dir := filepath.Dir(js.File)
	err := os.MkdirAll(dir, 0600)
	if err != nil {
		return err
	}

	file, err := os.Create(js.File)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	err = encoder.Encode(run)
	if err != nil {
		return err
	}

	return nil
}

func (js *JsonSerializer[OC, JC]) Deserialize() (Run[OC, JC], error) {
	file, err := os.Open(js.File)
	if err != nil {
		return Run[OC, JC]{}, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	var run Run[OC, JC]
	err = decoder.Decode(&run)
	if err != nil {
		var zero Run[OC, JC]
		return zero, err
	}

	return run, nil
}

// NewState creates a new state

// NewState creates a new state

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

func (p *Processor[AC, OC, JC]) Exec(ctx context.Context, r *Run[OC, JC]) error {
	// Make a map of triggers to states so we can easily reference it
	stateMap := map[string]State[AC, OC, JC]{}
	for _, s := range p.States {
		stateMap[s.TriggerState] = s
	}
	stateNames := make([]string, 0, len(stateMap))
	for k := range stateMap {
		stateNames = append(stateNames, k)
	}

	// When a job changes state, we send it to this channel to centrally manage and re-queue
	returnChan := make(chan Job[JC], 1000)

	// For each state, we need a channel of jobs
	stateChan := map[string]chan Job[JC]{}

	wg := sync.WaitGroup{}

	for _, s := range p.States {
		// Terminal states don't need to recieve jobs, they're just done
		if s.Terminal {
			continue
		}
		if s.Exec == nil {
			return fmt.Errorf("State [%s] has no executor, valid state names: %s", s.TriggerState, strings.Join(stateNames, ", "))
		}

		concurrency := s.Concurrency

		// Make a channel of that concurrency
		x := make(chan Job[JC], len(r.Jobs))
		stateChan[s.TriggerState] = x
		// Make workers for each, they just process and fire back to the central channel
		for i := 0; i < concurrency; i++ {
			wg.Add(1) // add a waiter for every go processor, do it before forking
			go func() {
				for j := range x {
					if s.RateLimit != nil {
						s.RateLimit.Wait(ctx)
					}
					// Execute the job
					j.C, j.State, _ = s.Exec(p.AppContext, r.Overall, j.C)
					returnChan <- j
				}
				log.Printf("Processor [%s] worker done", s.TriggerState)
				wg.Done()
			}()
		}
	}
	// wgReturn := sync.WaitGroup{}
	wg.Add(1)

	// Now we gotta kick off all of the states to their correct queue
	for _, job := range r.Jobs {
		// If it's in a terminal state, skip
		if stateMap[job.State].Terminal {
			continue
		}
		// Add the job to the state
		stateChan[job.State] <- job
	}

	// Make a central processor and start it
	go func() {
		for j := range returnChan {
			// Save the state
			r.Jobs[j.Id] = j

			// Sent the job to the next state channel
			nextState, ok := stateMap[j.State]
			if !ok {
				stateNames := make([]string, 0, len(stateMap))
				log.Fatalf("No state [%s] found in the state map, valid states, %s", j.State, strings.Join(stateNames, ", "))
			}
			// If it's terminal, we're done with this job
			if !nextState.Terminal {
				// We need to get the chan for the next one
				nextChan := stateChan[nextState.TriggerState]
				// Send the job to the next chan
				nextChan <- j
				continue
			}
			// If the state was terminal, we should see if all of the states are terminated, if so shut down
			shutdown := true
			for _, j := range r.Jobs {
				if !stateMap[j.State].Terminal {
					shutdown = false
					break
				}
			}
			if !shutdown {
				continue
			}
			log.Println("All jobs are terminal state, shutting down")
			// close all of the channels
			for _, c := range stateChan {
				close(c)
			}
			// close ourselves down
			close(returnChan)
			break
		}
		wg.Done()
	}()

	// Wait for all of the processors to quit
	wg.Wait()
	// close(returnChan)
	// wgReturn.Wait()

	return nil
}
