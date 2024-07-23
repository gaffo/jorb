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
	// Removes the monotonic clock portion of the timestamp which is only useful for measuring time
	// https://pkg.go.dev/time#hdr-Monotonic_Clocks
	// The monotonic clock information will not be marshalled, and thus cause tests which Marshal / Unmarshal job state
	// and expect the results to be the same to fail.
	t := time.Now().UTC().Truncate(time.Millisecond)
	// Set the LastUpdate field to the current time
	j.LastUpdate = &t
	return j
}
