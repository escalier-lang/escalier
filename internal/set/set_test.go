package set

import (
	"reflect"
	"sort"
	"testing"
)

func TestNewSet(t *testing.T) {
	s := NewSet[int]()
	if s == nil {
		t.Error("NewSet returned nil")
	}
	if s.Len() != 0 {
		t.Errorf("Expected empty set, got length %d", s.Len())
	}
}

func TestFromSlice(t *testing.T) {
	tests := []struct {
		name     string
		input    []int
		expected []int
	}{
		{
			name:     "empty slice",
			input:    []int{},
			expected: []int{},
		},
		{
			name:     "single element",
			input:    []int{1},
			expected: []int{1},
		},
		{
			name:     "multiple elements",
			input:    []int{1, 2, 3},
			expected: []int{1, 2, 3},
		},
		{
			name:     "duplicate elements",
			input:    []int{1, 2, 2, 3, 1},
			expected: []int{1, 2, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := FromSlice(tt.input)
			result := s.ToSlice()
			sort.Ints(result)
			sort.Ints(tt.expected)

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestAdd(t *testing.T) {
	s := NewSet[string]()

	// Add first element
	s.Add("hello")
	if !s.Contains("hello") {
		t.Error("Element not added to set")
	}
	if s.Len() != 1 {
		t.Errorf("Expected length 1, got %d", s.Len())
	}

	// Add same element again
	s.Add("hello")
	if s.Len() != 1 {
		t.Errorf("Expected length 1 after adding duplicate, got %d", s.Len())
	}

	// Add different element
	s.Add("world")
	if s.Len() != 2 {
		t.Errorf("Expected length 2, got %d", s.Len())
	}
}

func TestRemove(t *testing.T) {
	s := FromSlice([]string{"hello", "world", "test"})

	// Remove existing element
	s.Remove("hello")
	if s.Contains("hello") {
		t.Error("Element not removed from set")
	}
	if s.Len() != 2 {
		t.Errorf("Expected length 2, got %d", s.Len())
	}

	// Remove non-existing element (should not panic)
	s.Remove("nonexistent")
	if s.Len() != 2 {
		t.Errorf("Expected length 2 after removing non-existent element, got %d", s.Len())
	}

	// Remove all elements
	s.Remove("world")
	s.Remove("test")
	if s.Len() != 0 {
		t.Errorf("Expected empty set, got length %d", s.Len())
	}
}

func TestContains(t *testing.T) {
	s := FromSlice([]int{1, 2, 3, 5, 8})

	// Test existing elements
	existingElements := []int{1, 2, 3, 5, 8}
	for _, elem := range existingElements {
		if !s.Contains(elem) {
			t.Errorf("Expected set to contain %d", elem)
		}
	}

	// Test non-existing elements
	nonExistingElements := []int{0, 4, 6, 7, 9, 10}
	for _, elem := range nonExistingElements {
		if s.Contains(elem) {
			t.Errorf("Expected set to not contain %d", elem)
		}
	}
}

func TestLen(t *testing.T) {
	s := NewSet[int]()

	// Empty set
	if s.Len() != 0 {
		t.Errorf("Expected length 0, got %d", s.Len())
	}

	// Add elements and check length
	for i := 1; i <= 5; i++ {
		s.Add(i)
		if s.Len() != i {
			t.Errorf("Expected length %d, got %d", i, s.Len())
		}
	}

	// Add duplicate element
	s.Add(3)
	if s.Len() != 5 {
		t.Errorf("Expected length 5 after adding duplicate, got %d", s.Len())
	}
}

func TestToSlice(t *testing.T) {
	// Empty set
	emptySet := NewSet[int]()
	result := emptySet.ToSlice()
	if len(result) != 0 {
		t.Errorf("Expected empty slice, got length %d", len(result))
	}

	// Non-empty set
	elements := []int{3, 1, 4, 1, 5, 9, 2, 6, 5}
	s := FromSlice(elements)
	result = s.ToSlice()

	// Sort both slices for comparison
	sort.Ints(result)
	expected := []int{1, 2, 3, 4, 5, 6, 9}

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("Expected %v, got %v", expected, result)
	}
}

func TestClear(t *testing.T) {
	s := FromSlice([]string{"a", "b", "c", "d", "e"})

	if s.Len() != 5 {
		t.Errorf("Expected initial length 5, got %d", s.Len())
	}

	s.Clear()

	if s.Len() != 0 {
		t.Errorf("Expected length 0 after clear, got %d", s.Len())
	}

	// Verify no elements remain
	for _, elem := range []string{"a", "b", "c", "d", "e"} {
		if s.Contains(elem) {
			t.Errorf("Element %s still exists after clear", elem)
		}
	}
}

func TestUnion(t *testing.T) {
	tests := []struct {
		name     string
		set1     []int
		set2     []int
		expected []int
	}{
		{
			name:     "both empty",
			set1:     []int{},
			set2:     []int{},
			expected: []int{},
		},
		{
			name:     "one empty",
			set1:     []int{1, 2, 3},
			set2:     []int{},
			expected: []int{1, 2, 3},
		},
		{
			name:     "no overlap",
			set1:     []int{1, 2, 3},
			set2:     []int{4, 5, 6},
			expected: []int{1, 2, 3, 4, 5, 6},
		},
		{
			name:     "partial overlap",
			set1:     []int{1, 2, 3},
			set2:     []int{3, 4, 5},
			expected: []int{1, 2, 3, 4, 5},
		},
		{
			name:     "complete overlap",
			set1:     []int{1, 2, 3},
			set2:     []int{1, 2, 3},
			expected: []int{1, 2, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s1 := FromSlice(tt.set1)
			s2 := FromSlice(tt.set2)
			result := s1.Union(s2)

			resultSlice := result.ToSlice()
			sort.Ints(resultSlice)
			sort.Ints(tt.expected)

			if !reflect.DeepEqual(resultSlice, tt.expected) {
				t.Errorf("Expected %v, got %v", tt.expected, resultSlice)
			}
		})
	}
}

func TestIntersection(t *testing.T) {
	tests := []struct {
		name     string
		set1     []int
		set2     []int
		expected []int
	}{
		{
			name:     "both empty",
			set1:     []int{},
			set2:     []int{},
			expected: []int{},
		},
		{
			name:     "one empty",
			set1:     []int{1, 2, 3},
			set2:     []int{},
			expected: []int{},
		},
		{
			name:     "no overlap",
			set1:     []int{1, 2, 3},
			set2:     []int{4, 5, 6},
			expected: []int{},
		},
		{
			name:     "partial overlap",
			set1:     []int{1, 2, 3, 4},
			set2:     []int{3, 4, 5, 6},
			expected: []int{3, 4},
		},
		{
			name:     "complete overlap",
			set1:     []int{1, 2, 3},
			set2:     []int{1, 2, 3},
			expected: []int{1, 2, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s1 := FromSlice(tt.set1)
			s2 := FromSlice(tt.set2)
			result := s1.Intersection(s2)

			resultSlice := result.ToSlice()
			sort.Ints(resultSlice)
			sort.Ints(tt.expected)

			if !reflect.DeepEqual(resultSlice, tt.expected) {
				t.Errorf("Expected %v, got %v", tt.expected, resultSlice)
			}
		})
	}
}

func TestDifference(t *testing.T) {
	tests := []struct {
		name     string
		set1     []int
		set2     []int
		expected []int
	}{
		{
			name:     "both empty",
			set1:     []int{},
			set2:     []int{},
			expected: []int{},
		},
		{
			name:     "first empty",
			set1:     []int{},
			set2:     []int{1, 2, 3},
			expected: []int{},
		},
		{
			name:     "second empty",
			set1:     []int{1, 2, 3},
			set2:     []int{},
			expected: []int{1, 2, 3},
		},
		{
			name:     "no overlap",
			set1:     []int{1, 2, 3},
			set2:     []int{4, 5, 6},
			expected: []int{1, 2, 3},
		},
		{
			name:     "partial overlap",
			set1:     []int{1, 2, 3, 4},
			set2:     []int{3, 4, 5, 6},
			expected: []int{1, 2},
		},
		{
			name:     "complete overlap",
			set1:     []int{1, 2, 3},
			set2:     []int{1, 2, 3},
			expected: []int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s1 := FromSlice(tt.set1)
			s2 := FromSlice(tt.set2)
			result := s1.Difference(s2)

			resultSlice := result.ToSlice()
			sort.Ints(resultSlice)
			sort.Ints(tt.expected)

			if !reflect.DeepEqual(resultSlice, tt.expected) {
				t.Errorf("Expected %v, got %v", tt.expected, resultSlice)
			}
		})
	}
}

func TestIsSubset(t *testing.T) {
	tests := []struct {
		name     string
		set1     []int
		set2     []int
		expected bool
	}{
		{
			name:     "both empty",
			set1:     []int{},
			set2:     []int{},
			expected: true,
		},
		{
			name:     "empty subset of non-empty",
			set1:     []int{},
			set2:     []int{1, 2, 3},
			expected: true,
		},
		{
			name:     "non-empty subset of empty",
			set1:     []int{1, 2, 3},
			set2:     []int{},
			expected: false,
		},
		{
			name:     "proper subset",
			set1:     []int{1, 2},
			set2:     []int{1, 2, 3, 4},
			expected: true,
		},
		{
			name:     "not a subset",
			set1:     []int{1, 2, 5},
			set2:     []int{1, 2, 3, 4},
			expected: false,
		},
		{
			name:     "equal sets",
			set1:     []int{1, 2, 3},
			set2:     []int{1, 2, 3},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s1 := FromSlice(tt.set1)
			s2 := FromSlice(tt.set2)
			result := s1.IsSubset(s2)

			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestEquals(t *testing.T) {
	tests := []struct {
		name     string
		set1     []int
		set2     []int
		expected bool
	}{
		{
			name:     "both empty",
			set1:     []int{},
			set2:     []int{},
			expected: true,
		},
		{
			name:     "one empty, one not",
			set1:     []int{},
			set2:     []int{1, 2, 3},
			expected: false,
		},
		{
			name:     "different lengths",
			set1:     []int{1, 2},
			set2:     []int{1, 2, 3},
			expected: false,
		},
		{
			name:     "same length, different elements",
			set1:     []int{1, 2, 3},
			set2:     []int{1, 2, 4},
			expected: false,
		},
		{
			name:     "equal sets",
			set1:     []int{1, 2, 3},
			set2:     []int{3, 2, 1}, // Order shouldn't matter
			expected: true,
		},
		{
			name:     "equal sets with duplicates in input",
			set1:     []int{1, 2, 2, 3},
			set2:     []int{3, 1, 2},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s1 := FromSlice(tt.set1)
			s2 := FromSlice(tt.set2)
			result := s1.Equals(s2)

			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// Test with different types to ensure generics work properly
func TestGenericTypes(t *testing.T) {
	// Test with strings
	stringSet := FromSlice([]string{"apple", "banana", "cherry"})
	if !stringSet.Contains("apple") {
		t.Error("String set should contain 'apple'")
	}

	// Test with custom comparable type
	type CustomInt int
	customSet := FromSlice([]CustomInt{1, 2, 3})
	if !customSet.Contains(CustomInt(2)) {
		t.Error("Custom type set should contain CustomInt(2)")
	}
}

// Benchmark tests
func BenchmarkAdd(b *testing.B) {
	s := NewSet[int]()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Add(i)
	}
}

func BenchmarkContains(b *testing.B) {
	s := NewSet[int]()
	for i := 0; i < 1000; i++ {
		s.Add(i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Contains(i % 1000)
	}
}

func BenchmarkUnion(b *testing.B) {
	s1 := NewSet[int]()
	s2 := NewSet[int]()
	for i := 0; i < 500; i++ {
		s1.Add(i)
		s2.Add(i + 250)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s1.Union(s2)
	}
}
