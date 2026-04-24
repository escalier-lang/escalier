package set

// OrderedSet is a set that preserves insertion order.
type OrderedSet[T comparable] struct {
	items []T
	seen  map[T]struct{}
}

// NewOrderedSet creates a new empty OrderedSet.
func NewOrderedSet[T comparable]() *OrderedSet[T] {
	return &OrderedSet[T]{seen: make(map[T]struct{})}
}

// Add inserts an item if not already present.
func (s *OrderedSet[T]) Add(item T) {
	if _, exists := s.seen[item]; !exists {
		s.seen[item] = struct{}{}
		s.items = append(s.items, item)
	}
}

// Contains checks if an item is in the set.
func (s *OrderedSet[T]) Contains(item T) bool {
	_, exists := s.seen[item]
	return exists
}

// Len returns the number of elements in the set.
func (s *OrderedSet[T]) Len() int {
	return len(s.items)
}

// ToSlice returns the elements in insertion order. The returned slice aliases
// internal storage and must not be modified.
func (s *OrderedSet[T]) ToSlice() []T {
	return s.items
}
