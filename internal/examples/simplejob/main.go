package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"math/rand"
	"os"
	"time"

	"github.com/gaffo/jorb"
)

type oc struct{}
type ac struct{}
type jc struct{}

func main() {
	a := ac{}
	slog.SetLogLoggerLevel(slog.LevelWarn)

	states := []jorb.State[ac, oc, jc]{
		{
			TriggerState: "A",
			Exec: func(ctx context.Context, ac ac, oc oc, jc jc) (jc, string, []jorb.KickRequest[jc], error) {
				time.Sleep(time.Duration(rand.Intn(1000)) * time.Millisecond)
				return jc, "B", nil, nil
			},
			Concurrency: 5,
		},
		{
			TriggerState: "B",
			Exec: func(ctx context.Context, ac ac, oc oc, jc jc) (jc, string, []jorb.KickRequest[jc], error) {
				time.Sleep(time.Duration(rand.Intn(1000)) * time.Millisecond)
				return jc, "C", nil, nil
			},
			Concurrency: 4,
		},
		{
			TriggerState: "C",
			Exec: func(ctx context.Context, ac ac, oc oc, jc jc) (jc, string, []jorb.KickRequest[jc], error) {
				time.Sleep(time.Duration(rand.Intn(1000)) * time.Millisecond)
				return jc, "D", nil, nil
			},
			Concurrency: 3,
		},
		{
			TriggerState: "D",
			Terminal:     true,
		},
	}

	statePath := "example.state.json"
	ws, _, err := jorb.NewJsonSerializer[oc, jc](statePath)
	if err != nil {
		log.Fatal(err)
	}
	defer ws.Close()

	// Resume between process runs: reload the persisted run from checkpoint + JSONL.
	run, err := ws.Deserialize()
	if err != nil {
		log.Fatal(err)
	}

	listener := &fileListener{fileName: "example.status"}
	p, err := jorb.NewProcessor[ac, oc, jc](a, states, ws, listener)
	if err != nil {
		log.Fatal(err)
	}

	if len(run.Jobs) == 0 {
		for i := 0; i < 100; i++ {
			run.AddJobWithState(jc{}, "A")
		}
	}

	if err := p.Exec(context.Background(), run); err != nil {
		log.Fatal(err)
	}
}

// Serializes the status updates to a file
type fileListener struct {
	fileName string
}

func (f *fileListener) StatusUpdate(status []jorb.StatusCount) {
	buf := &bytes.Buffer{}

	encoder := json.NewEncoder(buf)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(status)

	_ = os.WriteFile(f.fileName, buf.Bytes(), 0644)
}
