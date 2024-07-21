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
	o := oc{}
	a := ac{}
	r := jorb.NewRun[oc, jc]("example", o)

	slog.SetLogLoggerLevel(slog.LevelWarn)
	for i := 0; i < 100; i++ {
		r.AddJobWithState(jc{}, "A")
	}

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

	serial := jorb.NewJsonSerializer[oc, jc]("example.state")
	listener := &fileListener{fileName: "example.status"}
	p := jorb.NewProcessor[ac, oc, jc](a, states, serial, listener)
	if err := p.Exec(context.Background(), r); err != nil {
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
