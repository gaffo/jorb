package jorb

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

func TestProcessorOneJob(t *testing.T) {
	oc := &MyOverallContext{}
	ac := &MyAppContext{}
	r := &NewRun[MyOverallContext, MyJobContext]("job", oc)
	for i := 0; i < 30; i++ {
		r.AddJob(&MyJobContext{
			Count: 0,
		})
	}
	p := NewProcessor[MyAppContext, MyOverallContext, MyJobContext](ac)
	p.Execute(r)
}