package jorb

import "time"

// Job represents the current processing state of any job
type Job[JC any] struct {
	Id          string              // Id is a unique identifier for the job
	C           JC                  // C holds the job specific context
	State       string              // State represents the current processing state of the job
	StateErrors map[string][]string // StateErrors is a map of errors that occurred in the current state
	LastUpdate  *time.Time          // The last time this job was fetched
}

// UpdateLastEvent updates the LastUpdate field of the Job struct to the current time.
func (j Job[JC]) UpdateLastEvent() Job[JC] {
	t := time.Now()
	// Set the LastUpdate field to the current time
	j.LastUpdate = &t
	return j
}
