package checker

// TODO: make this a sum type so that different error type can reference other
// types if necessary
type Error struct {
	message string
}
