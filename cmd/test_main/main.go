package main

import (
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/gaffo/jorb"
)

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
	STATE_DONE     = "done"
	STATE_MIDDLE   = "middle"
	STATE_DONE_TWO = "done_two"
)

func main() {
	oc := MyOverallContext{}
	ac := MyAppContext{}
	r := jorb.NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 100_000; i++ {
		r.AddJob(MyJobContext{
			Count: 0,
		})
	}
	states := []jorb.State[MyAppContext, MyOverallContext, MyJobContext]{
		jorb.State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: jorb.TRIGGER_STATE_NEW,
			Exec: func(ac MyAppContext, oc MyOverallContext, jc MyJobContext) (MyJobContext, string, error) {
				time.Sleep(time.Millisecond * time.Duration(rand.Intn(1000)))
				jc.Count += 1
				c := rand.Intn(2) == 0
				if c {
					return jc, STATE_DONE, nil
				}
				return jc, STATE_DONE_TWO, nil
			},
			Terminal:    false,
			Concurrency: 10_000,
		},
		jorb.State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_DONE_TWO,
			Exec:         nil,
			Terminal:     true,
		},
		jorb.State[MyAppContext, MyOverallContext, MyJobContext]{
			TriggerState: STATE_DONE,
			Exec:         nil,
			Terminal:     true,
		},
	}

	p := jorb.NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac, states)

	start := time.Now()
	err := p.Exec(r)
	if err != nil {
		log.Fatal(err)
	}
	delta := time.Since(start)
	fmt.Printf("Delta: %v\n", delta)

	stateCount := map[string]int{}
	for _, j := range r.Jobs {
		if j.C.Count != 1 {
			fmt.Printf("Bad Job Job Count: %d\n", j.C.Count)
		}
		stateCount[j.State] += 1
	}
	if stateCount[STATE_DONE_TWO] < 4000 {
		fmt.Printf("Bad State Done Two: %d\n", stateCount[STATE_DONE_TWO])
	}
	if stateCount[STATE_DONE] < 4000 {
		fmt.Printf("Bad State Done: %d\n", stateCount[STATE_DONE])
	}
}
