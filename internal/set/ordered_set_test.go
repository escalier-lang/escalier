package set

import (
	"reflect"
	"testing"
)

func TestOrderedSet_New(t *testing.T) {
	s := NewOrderedSet[int]()
	if s.Len() != 0 {
		t.Errorf("Expected empty set, got length %d", s.Len())
	}
	if got := s.ToSlice(); len(got) != 0 {
		t.Errorf("Expected empty slice, got %v", got)
	}
}

func TestOrderedSet_Add(t *testing.T) {
	s := NewOrderedSet[int]()
	s.Add(3)
	s.Add(1)
	s.Add(2)

	if s.Len() != 3 {
		t.Errorf("Expected length 3, got %d", s.Len())
	}

	expected := []int{3, 1, 2}
	if !reflect.DeepEqual(s.ToSlice(), expected) {
		t.Errorf("Expected %v, got %v", expected, s.ToSlice())
	}
}

func TestOrderedSet_AddDuplicates(t *testing.T) {
	s := NewOrderedSet[int]()
	s.Add(1)
	s.Add(2)
	s.Add(1)
	s.Add(3)
	s.Add(2)

	if s.Len() != 3 {
		t.Errorf("Expected length 3, got %d", s.Len())
	}

	expected := []int{1, 2, 3}
	if !reflect.DeepEqual(s.ToSlice(), expected) {
		t.Errorf("Expected %v, got %v", expected, s.ToSlice())
	}
}

func TestOrderedSet_Contains(t *testing.T) {
	s := NewOrderedSet[string]()
	s.Add("a")
	s.Add("b")

	if !s.Contains("a") {
		t.Error("Expected set to contain 'a'")
	}
	if !s.Contains("b") {
		t.Error("Expected set to contain 'b'")
	}
	if s.Contains("c") {
		t.Error("Expected set to not contain 'c'")
	}
}

func TestOrderedSet_ContainsEmpty(t *testing.T) {
	s := NewOrderedSet[int]()
	if s.Contains(1) {
		t.Error("Expected empty set to not contain 1")
	}
}

func TestOrderedSet_PreservesInsertionOrder(t *testing.T) {
	s := NewOrderedSet[int]()
	values := []int{5, 3, 8, 1, 9, 2, 7}
	for _, v := range values {
		s.Add(v)
	}

	if !reflect.DeepEqual(s.ToSlice(), values) {
		t.Errorf("Expected insertion order %v, got %v", values, s.ToSlice())
	}
}

func TestOrderedSet_ToSliceReturnsSameBackingSlice(t *testing.T) {
	// Verifies the documented aliasing guarantee: ToSlice returns internal
	// storage, so consecutive calls return the same backing array.
	s := NewOrderedSet[int]()
	s.Add(1)
	s.Add(2)

	slice1 := s.ToSlice()
	slice2 := s.ToSlice()
	if &slice1[0] != &slice2[0] {
		t.Error("Expected ToSlice to return the same backing slice")
	}
}
