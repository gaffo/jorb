package jorb

import (
	"testing"
)

// import "github.com/stretchr/testify/assert"

// JobContext represents my Job's context, eg the state of doing work
type MyJobContext struct {
	Count int
}

// MyOverallContext any non-job specific state that is important for the overall run
type MyOverallContext struct {

}

// MyAppContext is all of my application processing, clients, etc reference for the job processors
type MyAppContext struct {

}

const (
	STATE_DONE = "done"
)

func TestProcessorOneJob(t *testing.T) {
	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 30; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}

	newFunc := func(ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, error) {
		jc.Count += 1
		return jc, STATE_DONE, nil
	}

	states := []State[MyAppContext, MyOverallContext, MyJobContext]{
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: &newFunc,
			Terminal: false,
		},
		State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: TRIGGER_STATE_NEW,
			Exec: nil,
			Terminal: false,
		},
	}


	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states)
	p.Exec(r)

	for _, j := range r.Jobs {
		if j.C.Count != 1 {
			t.Errorf("Expected Job %s Count to be 1 but was %d", j.Id, j.C.Count)
		}
	}
}