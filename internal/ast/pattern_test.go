package ast

import (
	"reflect"
	"testing"

	"github.com/escalier-lang/escalier/internal/set"
)

// Helper function to create an empty span for testing
func emptySpan() Span {
	return Span{
		Start:    Location{Line: 0, Column: 0},
		End:      Location{Line: 0, Column: 0},
		SourceID: 0,
	}
}

func TestFindBindings(t *testing.T) {
	tests := []struct {
		name     string
		pat      Pat
		expected set.Set[string]
	}{
		{
			name:     "simple identifier pattern",
			pat:      NewIdentPat("x", nil, emptySpan()),
			expected: set.FromSlice([]string{"x"}),
		},
		{
			name: "tuple pattern with identifiers",
			pat: NewTuplePat([]Pat{
				NewIdentPat("a", nil, emptySpan()),
				NewIdentPat("b", nil, emptySpan()),
				NewIdentPat("c", nil, emptySpan()),
			}, emptySpan()),
			expected: set.FromSlice([]string{"a", "b", "c"}),
		},
		{
			name: "nested tuple pattern",
			pat: NewTuplePat([]Pat{
				NewIdentPat("x", nil, emptySpan()),
				NewTuplePat([]Pat{
					NewIdentPat("y", nil, emptySpan()),
					NewIdentPat("z", nil, emptySpan()),
				}, emptySpan()),
			}, emptySpan()),
			expected: set.FromSlice([]string{"x", "y", "z"}),
		},
		{
			name: "object pattern with key-value pairs",
			pat: NewObjectPat([]ObjPatElem{
				NewObjKeyValuePat(
					&Ident{Name: "key1", span: emptySpan()},
					NewIdentPat("value1", nil, emptySpan()),
					nil,
					emptySpan(),
				),
				NewObjKeyValuePat(
					&Ident{Name: "key2", span: emptySpan()},
					NewIdentPat("value2", nil, emptySpan()),
					nil,
					emptySpan(),
				),
			}, emptySpan()),
			expected: set.FromSlice([]string{"value1", "value2"}),
		},
		{
			name: "object pattern with shorthand",
			pat: NewObjectPat([]ObjPatElem{
				NewObjShorthandPat(
					&Ident{Name: "shorthand1", span: emptySpan()},
					nil,
					emptySpan(),
				),
				NewObjShorthandPat(
					&Ident{Name: "shorthand2", span: emptySpan()},
					nil,
					emptySpan(),
				),
			}, emptySpan()),
			expected: set.FromSlice([]string{"shorthand1", "shorthand2"}),
		},
		{
			name: "object pattern with rest",
			pat: NewObjectPat([]ObjPatElem{
				NewObjKeyValuePat(
					&Ident{Name: "key", span: emptySpan()},
					NewIdentPat("value", nil, emptySpan()),
					nil,
					emptySpan(),
				),
				NewObjRestPat(
					NewIdentPat("rest", nil, emptySpan()),
					emptySpan(),
				),
			}, emptySpan()),
			expected: set.FromSlice([]string{"value", "rest"}),
		},
		{
			name: "rest pattern",
			pat: NewRestPat(
				NewIdentPat("rest", nil, emptySpan()),
				emptySpan(),
			),
			expected: set.FromSlice([]string{"rest"}),
		},
		{
			name: "extractor pattern",
			pat: NewExtractorPat("Some", []Pat{
				NewIdentPat("inner", nil, emptySpan()),
			}, emptySpan()),
			expected: set.FromSlice([]string{"inner"}),
		},
		{
			name: "nested extractor pattern",
			pat: NewExtractorPat("Result", []Pat{
				NewExtractorPat("Ok", []Pat{
					NewIdentPat("value", nil, emptySpan()),
				}, emptySpan()),
			}, emptySpan()),
			expected: set.FromSlice([]string{"value"}),
		},
		{
			name: "literal pattern",
			pat: NewLitPat(
				NewNumber(42, emptySpan()),
				emptySpan(),
			),
			expected: set.FromSlice([]string{}), // Literal patterns don't create bindings
		},
		{
			name:     "wildcard pattern",
			pat:      NewWildcardPat(emptySpan()),
			expected: set.FromSlice([]string{}), // Wildcard patterns don't create bindings
		},
		{
			name: "complex nested pattern",
			pat: NewTuplePat([]Pat{
				NewIdentPat("first", nil, emptySpan()),
				NewObjectPat([]ObjPatElem{
					NewObjKeyValuePat(
						&Ident{Name: "nested", span: emptySpan()},
						NewTuplePat([]Pat{
							NewIdentPat("x", nil, emptySpan()),
							NewIdentPat("y", nil, emptySpan()),
						}, emptySpan()),
						nil,
						emptySpan(),
					),
					NewObjRestPat(
						NewIdentPat("objRest", nil, emptySpan()),
						emptySpan(),
					),
				}, emptySpan()),
				NewRestPat(
					NewIdentPat("tupleRest", nil, emptySpan()),
					emptySpan(),
				),
			}, emptySpan()),
			expected: set.FromSlice([]string{"first", "x", "y", "objRest", "tupleRest"}),
		},
		{
			name: "mixed patterns with literals and wildcards",
			pat: NewTuplePat([]Pat{
				NewIdentPat("valid", nil, emptySpan()),
				NewLitPat(NewString("literal", emptySpan()), emptySpan()),
				NewWildcardPat(emptySpan()),
				NewIdentPat("another", nil, emptySpan()),
			}, emptySpan()),
			expected: set.FromSlice([]string{"valid", "another"}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FindBindings(tt.pat)

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("FindBindings() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestFindBindingsOrder(t *testing.T) {
	// Test that bindings are returned in the order they are encountered
	pat := NewTuplePat([]Pat{
		NewIdentPat("third", nil, emptySpan()),  // This should be first in result
		NewIdentPat("first", nil, emptySpan()),  // This should be second in result
		NewIdentPat("second", nil, emptySpan()), // This should be third in result
	}, emptySpan())

	result := FindBindings(pat)
	expected := set.FromSlice([]string{"third", "first", "second"})

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("FindBindings() order = %v, expected %v", result, expected)
	}
}

func TestFindBindingsNoDuplicates(t *testing.T) {
	// Test behavior with duplicate identifier names
	pat := NewTuplePat([]Pat{
		NewIdentPat("x", nil, emptySpan()),
		NewIdentPat("x", nil, emptySpan()), // Duplicate name
		NewIdentPat("y", nil, emptySpan()),
	}, emptySpan())

	result := FindBindings(pat)
	expected := set.FromSlice([]string{"x", "y"})

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("FindBindings() with duplicates = %v, expected %v", result, expected)
	}
}
