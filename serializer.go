package jorb

// Serializer is how [Processor] records job updates between checkpoints. [JsonSerializer] implements it
// with append-only JSONL; checkpoints are scheduled internally (see [JsonSerializer.CheckpointSync] for a synchronous flush).
type Serializer[OC any, JC any] interface {
	JobUpdate(job Job[JC]) error
	Deserialize() (*Run[OC, JC], error)
}

// NilSerializer is a no-op [Serializer]: it does not persist to disk.
type NilSerializer[OC any, JC any] struct{}

func (n *NilSerializer[OC, JC]) JobUpdate(job Job[JC]) error {
	_ = job
	return nil
}

func (n *NilSerializer[OC, JC]) Deserialize() (*Run[OC, JC], error) {
	panic("not implemented, shouldn't be called")
}

var _ Serializer[any, any] = (*NilSerializer[any, any])(nil)
