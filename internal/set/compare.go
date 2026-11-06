package set

// IsSubset checks if s is a subset of rhs.
func (s Set[T]) IsSubset(rhs Set[T]) bool {
	for item := range s {
		if !rhs.Contains(item) {
			return false
		}
	}
	return true
}

// IsSuperset checks if s is a superset of rhs.
func (s Set[T]) IsSuperset(rhs Set[T]) bool {
	return s.IsSubset(rhs)
}

// Equals checks if s and rhs hold the same items.
func (s Set[T]) Equals(rhs Set[T]) bool {
	if s.Len() != rhs.Len() {
		return false
	}
	for item := range s {
		if !rhs.Contains(item) {
			return false
		}
	}
	return true
}
