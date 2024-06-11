package jorb

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Serializer is an interface for job run seralization
type Serializer[OC any, JC any] interface {
	Serialize(r Run[OC, JC]) error
	Deserialize() (*Run[OC, JC], error)
}

// JsonSerializer is a struct that implements Serializer and stores and loads run from a file specified
// in the File field, there  is a anonymous variable type check
type JsonSerializer[OC any, JC any] struct {
	File string
}

// NewJsonSerializer create a new instance of the JsonSerializer struct.
// It takes a single argument `file` of type string, which represents the file path where the serialized
// run data will be stored or loaded from.
func NewJsonSerializer[OC any, JC any](file string) *JsonSerializer[OC, JC] {
	return &JsonSerializer[OC, JC]{
		File: file,
	}
}

var _ Serializer[any, any] = (*JsonSerializer[any, any])(nil)

// Serialize takes a Run[OC, JC] instance and serializes it to JSON format,
// writing the serialized data to the file specified when creating the JsonSerializer instance.
// It creates the parent directory for the file if it doesn't exist, and creates the file if it doesn't exist.
//
// If any error occurs during the process, such as creating the directory, creating the file,
// or encoding the Run instance, the function returns the error.
//
// Parameters:
//
//	run Run[OC, JC]: The Run instance to be serialized.
//
// Returns:
//
//	error: An error value if the serialization or file writing operation fails, otherwise nil.
func (js JsonSerializer[OC, JC]) Serialize(run Run[OC, JC]) error {
	// Create the parent directory if it doesn't exist
	dir := filepath.Dir(js.File)
	err := os.MkdirAll(dir, 0600)
	if err != nil {
		return err
	}

	file, err := os.Create(js.File)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	err = encoder.Encode(run)
	if err != nil {
		return err
	}

	return nil
}

// Deserialize reads the serialized Run[OC, JC] data from the file specified when creating the JsonSerializer instance,
// deserializes the JSON data into a Run[OC, JC] instance, and returns the deserialized Run instance.
//
// If any error occurs during the process, such as opening the file or decoding the JSON data,
// the function returns a zero-value Run[OC, JC] instance and the error.
//
// Returns:
//
//	Run[OC, JC]: The deserialized Run instance.
//	error: An error value if the deserialization or file reading operation fails, otherwise nil.
func (js JsonSerializer[OC, JC]) Deserialize() (*Run[OC, JC], error) {
	file, err := os.Open(js.File)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	var run Run[OC, JC]
	err = decoder.Decode(&run)
	if err != nil {
		return nil, err
	}

	return &run, nil
}

// NilSerializer implements the Serializer interface with no-op implementations
// of the Serialize and Deserialize methods. It is useful when you don't need to persist or load
// Run instances, and is used as the default by NewProcessor if you don't specify one
type NilSerializer[OC any, JC any] struct {
}

// Serialize is a no-op implementation that does nothing and always returns nil.
// It satisfies the Serializer interface's Serialize method requirement.
func (n *NilSerializer[OC, JC]) Serialize(run Run[OC, JC]) error {
	return nil
}

// Deserialize is a no-op implementation that panics with a "not implemented" message.
// It satisfies the Serializer interface's Deserialize method requirement, but it should
// never be called in practice when using the NilSerializer.
func (n *NilSerializer[OC, JC]) Deserialize() (*Run[OC, JC], error) {
	panic("not implemented, shouldn't be called")
}
