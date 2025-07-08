package codegen

import (
	"reflect"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
)

// Helper function to create an empty span for testing
func emptySpan() ast.Span {
	return ast.Span{}
}

func TestFindBindings(t *testing.T) {
	tests := []struct {
		name     string
		pat      ast.Pat
		expected []string
	}{
		{
			name:     "simple identifier pattern",
			pat:      ast.NewIdentPat("x", nil, emptySpan()),
			expected: []string{"x"},
		},
		{
			name: "tuple pattern with identifiers",
			pat: ast.NewTuplePat([]ast.Pat{
				ast.NewIdentPat("a", nil, emptySpan()),
				ast.NewIdentPat("b", nil, emptySpan()),
				ast.NewIdentPat("c", nil, emptySpan()),
			}, emptySpan()),
			expected: []string{"a", "b", "c"},
		},
		{
			name: "nested tuple pattern",
			pat: ast.NewTuplePat([]ast.Pat{
				ast.NewIdentPat("x", nil, emptySpan()),
				ast.NewTuplePat([]ast.Pat{
					ast.NewIdentPat("y", nil, emptySpan()),
					ast.NewIdentPat("z", nil, emptySpan()),
				}, emptySpan()),
			}, emptySpan()),
			expected: []string{"x", "y", "z"},
		},
		{
			name: "object pattern with key-value pairs",
			pat: ast.NewObjectPat([]ast.ObjPatElem{
				ast.NewObjKeyValuePat(
					&ast.Ident{Name: "key1"},
					ast.NewIdentPat("value1", nil, emptySpan()),
					nil,
					emptySpan(),
				),
				ast.NewObjKeyValuePat(
					&ast.Ident{Name: "key2"},
					ast.NewIdentPat("value2", nil, emptySpan()),
					nil,
					emptySpan(),
				),
			}, emptySpan()),
			expected: []string{"value1", "value2"},
		},
		{
			name: "object pattern with shorthand",
			pat: ast.NewObjectPat([]ast.ObjPatElem{
				ast.NewObjShorthandPat(
					&ast.Ident{Name: "shorthand1"},
					nil,
					emptySpan(),
				),
				ast.NewObjShorthandPat(
					&ast.Ident{Name: "shorthand2"},
					nil,
					emptySpan(),
				),
			}, emptySpan()),
			expected: []string{"shorthand1", "shorthand2"},
		},
		{
			name: "object pattern with rest",
			pat: ast.NewObjectPat([]ast.ObjPatElem{
				ast.NewObjKeyValuePat(
					&ast.Ident{Name: "key"},
					ast.NewIdentPat("value", nil, emptySpan()),
					nil,
					emptySpan(),
				),
				ast.NewObjRestPat(
					ast.NewIdentPat("rest", nil, emptySpan()),
					emptySpan(),
				),
			}, emptySpan()),
			expected: []string{"value", "rest"},
		},
		{
			name: "rest pattern",
			pat: ast.NewRestPat(
				ast.NewIdentPat("rest", nil, emptySpan()),
				emptySpan(),
			),
			expected: []string{"rest"},
		},
		{
			name: "extractor pattern",
			pat: ast.NewExtractorPat("Some", []ast.Pat{
				ast.NewIdentPat("inner", nil, emptySpan()),
			}, emptySpan()),
			expected: []string{"inner"},
		},
		{
			name: "nested extractor pattern",
			pat: ast.NewExtractorPat("Result", []ast.Pat{
				ast.NewExtractorPat("Ok", []ast.Pat{
					ast.NewIdentPat("value", nil, emptySpan()),
				}, emptySpan()),
			}, emptySpan()),
			expected: []string{"value"},
		},
		{
			name: "literal pattern",
			pat: ast.NewLitPat(
				ast.NewNumber(42, emptySpan()),
				emptySpan(),
			),
			expected: []string{}, // Literal patterns don't create bindings
		},
		{
			name:     "wildcard pattern",
			pat:      ast.NewWildcardPat(emptySpan()),
			expected: []string{}, // Wildcard patterns don't create bindings
		},
		{
			name: "complex nested pattern",
			pat: ast.NewTuplePat([]ast.Pat{
				ast.NewIdentPat("first", nil, emptySpan()),
				ast.NewObjectPat([]ast.ObjPatElem{
					ast.NewObjKeyValuePat(
						&ast.Ident{Name: "nested"},
						ast.NewTuplePat([]ast.Pat{
							ast.NewIdentPat("x", nil, emptySpan()),
							ast.NewIdentPat("y", nil, emptySpan()),
						}, emptySpan()),
						nil,
						emptySpan(),
					),
					ast.NewObjRestPat(
						ast.NewIdentPat("objRest", nil, emptySpan()),
						emptySpan(),
					),
				}, emptySpan()),
				ast.NewRestPat(
					ast.NewIdentPat("tupleRest", nil, emptySpan()),
					emptySpan(),
				),
			}, emptySpan()),
			expected: []string{"first", "x", "y", "objRest", "tupleRest"},
		},
		{
			name: "mixed patterns with literals and wildcards",
			pat: ast.NewTuplePat([]ast.Pat{
				ast.NewIdentPat("valid", nil, emptySpan()),
				ast.NewLitPat(ast.NewString("literal", emptySpan()), emptySpan()),
				ast.NewWildcardPat(emptySpan()),
				ast.NewIdentPat("another", nil, emptySpan()),
			}, emptySpan()),
			expected: []string{"valid", "another"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findBindings(tt.pat)

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("findBindings() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestFindBindingsOrder(t *testing.T) {
	// Test that bindings are returned in the order they are encountered
	pat := ast.NewTuplePat([]ast.Pat{
		ast.NewIdentPat("third", nil, emptySpan()),  // This should be first in result
		ast.NewIdentPat("first", nil, emptySpan()),  // This should be second in result
		ast.NewIdentPat("second", nil, emptySpan()), // This should be third in result
	}, emptySpan())

	result := findBindings(pat)
	expected := []string{"third", "first", "second"}

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("findBindings() order = %v, expected %v", result, expected)
	}
}

func TestFindBindingsDuplicates(t *testing.T) {
	// Test behavior with duplicate identifier names
	pat := ast.NewTuplePat([]ast.Pat{
		ast.NewIdentPat("x", nil, emptySpan()),
		ast.NewIdentPat("x", nil, emptySpan()), // Duplicate name
		ast.NewIdentPat("y", nil, emptySpan()),
	}, emptySpan())

	result := findBindings(pat)
	expected := []string{"x", "x", "y"} // Should include duplicates

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("findBindings() with duplicates = %v, expected %v", result, expected)
	}
}
