package set

type Set[T comparable] map[T]struct{}

// NewSet creates a new empty Set.
func NewSet[T comparable]() Set[T] {
	return make(Set[T])
}

// FromSlice creates a new Set from a slice.
func FromSlice[T comparable](items []T) Set[T] {
	s := NewSet[T]()
	for _, item := range items {
		s.Add(item)
	}
	return s
}

// Add inserts an item into the set.
func (s Set[T]) Add(item T) {
	s[item] = struct{}{}
}

// Remove deletes an item from the set.
func (s Set[T]) Remove(item T) {
	delete(s, item)
}

// Contains checks if an item is in the set.
func (s Set[T]) Contains(item T) bool {
	_, exists := s[item]
	return exists
}

// Len returns the number of elements in the set.
func (s Set[T]) Len() int {
	return len(s)
}

// ToSlice returns a slice of all elements in the set.
func (s Set[T]) ToSlice() []T {
	result := make([]T, 0, len(s))
	for item := range s {
		result = append(result, item)
	}
	return result
}

// Clear removes all elements from the set.
func (s Set[T]) Clear() {
	for item := range s {
		delete(s, item)
	}
}

// Union returns a new set that is the union of s and other.
func (s Set[T]) Union(other Set[T]) Set[T] {
	result := NewSet[T]()
	for item := range s {
		result.Add(item)
	}
	for item := range other {
		result.Add(item)
	}
	return result
}

// Intersection returns a new set that is the intersection of s and other.
func (s Set[T]) Intersection(other Set[T]) Set[T] {
	result := NewSet[T]()
	for item := range s {
		if other.Contains(item) {
			result.Add(item)
		}
	}
	return result
}

// Difference returns a new set with elements in s but not in other.
func (s Set[T]) Difference(other Set[T]) Set[T] {
	result := NewSet[T]()
	for item := range s {
		if !other.Contains(item) {
			result.Add(item)
		}
	}
	return result
}

// IsSubset checks if s is a subset of other.
func (s Set[T]) IsSubset(other Set[T]) bool {
	for item := range s {
		if !other.Contains(item) {
			return false
		}
	}
	return true
}

// Equals checks if two sets are equal.
func (s Set[T]) Equals(other Set[T]) bool {
	if s.Len() != other.Len() {
		return false
	}
	for item := range s {
		if !other.Contains(item) {
			return false
		}
	}
	return true
}
