package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"

	"github.com/gaffo/jorb"
)

type AppContext struct{}
type OverallContext struct{}
type JobContext struct {
	Value int
}

func main() {
	// Create states with AIMD rate limiter
	states := []jorb.State[AppContext, OverallContext, JobContext]{
		{
			TriggerState: "process",
			Exec:         processJob,
			Concurrency:  5,
			// Start at 10 req/s, min 1 req/s, max 50 req/s
			RateLimit: jorb.NewAIMDRateLimiter(10, 1, 50),
		},
		{
			TriggerState: "done",
			Terminal:     true,
		},
	}

	const statePath = "aimd.state.json"
	ws, _, err := jorb.NewJsonSerializer[OverallContext, JobContext](statePath)
	if err != nil {
		log.Fatal(err)
	}
	defer ws.Close()

	// Resume between process runs: reload the persisted run from disk.
	run, err := ws.Deserialize()
	if err != nil {
		log.Fatal(err)
	}

	// Create processor
	processor, err := jorb.NewProcessor(
		AppContext{},
		states,
		ws,
		&jorb.NilStatusListener{},
	)
	if err != nil {
		panic(err)
	}

	if len(run.Jobs) == 0 {
		run.Name = "example-run"
		for i := 0; i < 100; i++ {
			run.AddJobWithState(JobContext{Value: i}, "process")
		}
	}

	// Execute
	if err := processor.Exec(context.Background(), run); err != nil {
		panic(err)
	}

	fmt.Println("All jobs completed!")
}

func processJob(ctx context.Context, ac AppContext, oc OverallContext, jc JobContext) (JobContext, string, []jorb.KickRequest[JobContext], error) {
	// Simulate occasional rate limit errors (10% chance)
	if rand.Float64() < 0.1 {
		return jc, "process", nil, &jorb.RateLimitError{
			Err: fmt.Errorf("rate limit exceeded for job %d", jc.Value),
		}
	}

	// Process successfully
	return jc, "done", nil, nil
}
