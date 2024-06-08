package jorb

// Job represents the current processing state of any job
type Job[JC any] struct {
}

// Run run manages state of the work a processor is doing
type Run[OC any, JC any] struct {
	Name string
	Jobs map[string]Job[JC]
	Overall OC
}

func NewRun[OC any, JC any](name string, OC oc) {
	return &Run{
		Name: name,
		Jobs: map[string]Job[JC]{},
		Overall: oc,
	}
}

// Add a job to the pool, this shouldn't be called once it's running
func (r *Run[OC any, JC any]) AddJob(JC jc) {
	// TODO: Use a uuid for the jobs
	r.Jobs[fmt.Sprintf("%d", len(r.Jobs)) = jc
}

// Processor executes a job
type Processor[AC any, OC any, JC any] struct {
}

func NewProcessor[AC any, OC any, JC any]r() (*Processor[AC, OC, JC]){
	return &Processor[AC,OC,JC]{
	}
}

func (p *Processor[AC any, OC any, JC any]) Exec(Run[OC,JC]) {

}