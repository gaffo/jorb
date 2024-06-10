package jorb

// StatusListener is an interface that defines a method for receiving status updates.
// It is used by the processor to notify interested parties about the current status
// of job processing.
type StatusListener interface {
	// StatusUpdate is called by the processor to provide an update on the current
	// status of job processing. The `status` parameter is a slice of StatusCount
	// instances, where each instance represents the count of jobs in a particular state.
	//
	// The status counts will be in the same order as the states passed to the processor
	StatusUpdate(status []StatusCount)
}

// NilStatusListener is a struct that implements the StatusListener interface with a no-op
// implementation of the StatusUpdate method. It is useful when you don't need to receive
// status updates or when you want to use a dummy status listener.
type NilStatusListener struct {
}

// StatusUpdate is a no-op implementation that does nothing. It satisfies the StatusListener
// interface's StatusUpdate method requirement.
func (n NilStatusListener) StatusUpdate(status []StatusCount) {
}

// This line ensures that the NilStatusListener struct implements the StatusListener interface.
// It is a way to verify at compile-time that the NilStatusListener struct correctly implements
// the required methods of the StatusListener interface.
var _ StatusListener = &NilStatusListener{}
