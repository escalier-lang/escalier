package checker

// TODO: make this a sum type so that different error type can reference other
// types if necessary
type Error struct {
	Message string
	// TODO: include a field for the source of the error
}
