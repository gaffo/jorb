package jorb

import "time"

// cloneStateErrorsMap returns a deep copy so workers can append errors without racing
// concurrent JSON serialization of the Run that still holds the original job map.
func cloneStateErrorsMap(m map[string][]string) map[string][]string {
	if m == nil {
		return map[string][]string{}
	}
	out := make(map[string][]string, len(m))
	for k, v := range m {
		s := make([]string, len(v))
		copy(s, v)
		out[k] = s
	}
	return out
}

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
	t := time.Now().Truncate(time.Millisecond)
	// Set the LastUpdate field to the current time
	j.LastUpdate = &t
	return j
}
