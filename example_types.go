package jorb

// Example overall/job/app context types shared by tests, examples, and benchmarks.

// MyJobContext represents sample job-specific state used in tests and examples.
type MyJobContext struct {
	Name       string
	Count      int
	StringList []string
	String     string
}

// MyOverallContext holds non-job-specific run-level state used in tests and examples.
type MyOverallContext struct {
	Name string
}

// MyAppContext is placeholder application context used in processor tests.
type MyAppContext struct{}
