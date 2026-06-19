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
			pat:      NewIdentPat("x", false, nil, nil, emptySpan()),
			expected: set.FromSlice([]string{"x"}),
		},
		{
			name: "tuple pattern with identifiers",
			pat: NewTuplePat([]Pat{
				NewIdentPat("a", false, nil, nil, emptySpan()),
				NewIdentPat("b", false, nil, nil, emptySpan()),
				NewIdentPat("c", false, nil, nil, emptySpan()),
			}, emptySpan()),
			expected: set.FromSlice([]string{"a", "b", "c"}),
		},
		{
			name: "nested tuple pattern",
			pat: NewTuplePat([]Pat{
				NewIdentPat("x", false, nil, nil, emptySpan()),
				NewTuplePat([]Pat{
					NewIdentPat("y", false, nil, nil, emptySpan()),
					NewIdentPat("z", false, nil, nil, emptySpan()),
				}, emptySpan()),
			}, emptySpan()),
			expected: set.FromSlice([]string{"x", "y", "z"}),
		},
		{
			name: "object pattern with key-value pairs",
			pat: NewObjectPat([]ObjPatElem{
				NewObjKeyValuePat(
					&Ident{Name: "key1", span: emptySpan()},
					NewIdentPat("value1", false, nil, nil, emptySpan()),
					emptySpan(),
				),
				NewObjKeyValuePat(
					&Ident{Name: "key2", span: emptySpan()},
					NewIdentPat("value2", false, nil, nil, emptySpan()),
					emptySpan(),
				),
			}, emptySpan()),
			expected: set.FromSlice([]string{"value1", "value2"}),
		},
		{
			name: "object pattern with shorthand",
			pat: NewObjectPat([]ObjPatElem{
				NewObjShorthandPat(
					&Ident{Name: "shorthand1", span: emptySpan()}, false,

					nil,
					nil,
					emptySpan()),

				NewObjShorthandPat(
					&Ident{Name: "shorthand2", span: emptySpan()}, false,

					nil,
					nil,
					emptySpan()),
			}, emptySpan()),
			expected: set.FromSlice([]string{"shorthand1", "shorthand2"}),
		},
		{
			name: "object pattern with rest",
			pat: NewObjectPat([]ObjPatElem{
				NewObjKeyValuePat(
					&Ident{Name: "key", span: emptySpan()},
					NewIdentPat("value", false, nil, nil, emptySpan()),
					emptySpan(),
				),
				NewObjRestPat(
					NewIdentPat("rest", false, nil, nil, emptySpan()),
					emptySpan(),
				),
			}, emptySpan()),
			expected: set.FromSlice([]string{"value", "rest"}),
		},
		{
			name: "rest pattern",
			pat: NewRestPat(
				NewIdentPat("rest", false, nil, nil, emptySpan()),
				emptySpan(),
			),
			expected: set.FromSlice([]string{"rest"}),
		},
		{
			name: "extractor pattern",
			pat: NewExtractorPat(NewIdentifier("Some", emptySpan()), []Pat{
				NewIdentPat("inner", false, nil, nil, emptySpan()),
			}, emptySpan()),
			expected: set.FromSlice([]string{"inner"}),
		},
		{
			name: "nested extractor pattern",
			pat: NewExtractorPat(NewIdentifier("Result", emptySpan()), []Pat{
				NewExtractorPat(NewIdentifier("Ok", emptySpan()), []Pat{
					NewIdentPat("value", false, nil, nil, emptySpan()),
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
				NewIdentPat("first", false, nil, nil, emptySpan()),
				NewObjectPat([]ObjPatElem{
					NewObjKeyValuePat(
						&Ident{Name: "nested", span: emptySpan()},
						NewTuplePat([]Pat{
							NewIdentPat("x", false, nil, nil, emptySpan()),
							NewIdentPat("y", false, nil, nil, emptySpan()),
						}, emptySpan()),
						emptySpan(),
					),
					NewObjRestPat(
						NewIdentPat("objRest", false, nil, nil, emptySpan()),
						emptySpan(),
					),
				}, emptySpan()),
				NewRestPat(
					NewIdentPat("tupleRest", false, nil, nil, emptySpan()),
					emptySpan(),
				),
			}, emptySpan()),
			expected: set.FromSlice([]string{"first", "x", "y", "objRest", "tupleRest"}),
		},
		{
			name: "mixed patterns with literals and wildcards",
			pat: NewTuplePat([]Pat{
				NewIdentPat("valid", false, nil, nil, emptySpan()),
				NewLitPat(NewString("literal", emptySpan()), emptySpan()),
				NewWildcardPat(emptySpan()),
				NewIdentPat("another", false, nil, nil, emptySpan()),
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
		NewIdentPat("third", false, nil, nil, emptySpan()),
		NewIdentPat("first", false, nil, nil, emptySpan()),
		NewIdentPat("second", false, nil, nil, emptySpan()),
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
		NewIdentPat("x", false, nil, nil, emptySpan()),
		NewIdentPat("x", false, nil, nil, emptySpan()),
		NewIdentPat("y", false, nil, nil, emptySpan()),
	}, emptySpan())

	result := FindBindings(pat)
	expected := set.FromSlice([]string{"x", "y"})

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("FindBindings() with duplicates = %v, expected %v", result, expected)
	}
}

// TestNewIdentPatMutableArg locks in the Mutable parameter on
// NewIdentPat. The ctor previously took only (name, typeAnn, default,
// span) and required callers to set .Mutable as a follow-up — which the
// codegen IdentPat-copy path silently forgot to do (builder.go:449).
// Forcing Mutable through the ctor makes the invariant impossible to
// drop on accident.
func TestNewIdentPatMutableArg(t *testing.T) {
	mutPat := NewIdentPat("x", true, nil, nil, emptySpan())
	if !mutPat.Mutable {
		t.Errorf("expected Mutable=true on ctor-built pat, got false")
	}
	plainPat := NewIdentPat("x", false, nil, nil, emptySpan())
	if plainPat.Mutable {
		t.Errorf("expected Mutable=false on ctor-built pat, got true")
	}
}

// TestNewObjShorthandPatMutableArg is the parallel guarantee for
// NewObjShorthandPat — shorthand `mut` (`{ mut x }`) must round-trip
// through the ctor.
func TestNewObjShorthandPatMutableArg(t *testing.T) {
	key := &Ident{Name: "x", span: emptySpan()}
	mutPat := NewObjShorthandPat(key, true, nil, nil, emptySpan())
	if !mutPat.Mutable {
		t.Errorf("expected Mutable=true on ctor-built pat, got false")
	}
	plainPat := NewObjShorthandPat(key, false, nil, nil, emptySpan())
	if plainPat.Mutable {
		t.Errorf("expected Mutable=false on ctor-built pat, got true")
	}
}

func TestForEachLeafBinding(t *testing.T) {
	ident := func(n string) *IdentPat { return NewIdentPat(n, false, nil, nil, emptySpan()) }
	shorthand := func(n string) ObjPatElem {
		return NewObjShorthandPat(&Ident{Name: n, span: emptySpan()}, false, nil, nil, emptySpan())
	}
	collect := func(pat Pat) []string {
		var names []string
		ForEachLeafBinding(pat, func(name string, _ int) { names = append(names, name) })
		return names
	}

	tests := []struct {
		name string
		pat  Pat
		want []string
	}{
		{"ident", ident("x"), []string{"x"}},
		{"tuple", NewTuplePat([]Pat{ident("a"), ident("b")}, emptySpan()), []string{"a", "b"}},
		{"object shorthand", NewObjectPat([]ObjPatElem{shorthand("a"), shorthand("b")}, emptySpan()), []string{"a", "b"}},
		{"rest", NewRestPat(ident("r"), emptySpan()), []string{"r"}},
		// ExtractorPat: each argument is a sub-pattern (e.g. `Some(v, w)`).
		{
			"extractor args",
			NewExtractorPat(NewIdentifier("Some", emptySpan()), []Pat{ident("v"), ident("w")}, emptySpan()),
			[]string{"v", "w"},
		},
		// InstancePat: leaves live in the object sub-pattern (e.g. `Point { x, y }`).
		{
			"instance object",
			NewInstancePat(
				NewIdentifier("Point", emptySpan()),
				NewObjectPat([]ObjPatElem{shorthand("x"), shorthand("y")}, emptySpan()),
				emptySpan(),
			),
			[]string{"x", "y"},
		},
		// Composite patterns nest: an extractor inside a tuple.
		{
			"nested extractor in tuple",
			NewTuplePat([]Pat{
				ident("a"),
				NewExtractorPat(NewIdentifier("Wrap", emptySpan()), []Pat{ident("b")}, emptySpan()),
			}, emptySpan()),
			[]string{"a", "b"},
		},
		// Non-binding patterns contribute no leaves.
		{"wildcard binds nothing", NewWildcardPat(emptySpan()), nil},
		{"literal binds nothing", NewLitPat(NewNumber(1, emptySpan()), emptySpan()), nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := collect(tc.pat); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ForEachLeafBinding leaves = %v, want %v", got, tc.want)
			}
		})
	}
}

// ForEachLeafBinding passes through each leaf's VarID, including for leaves reached
// through an extractor pattern's arguments.
func TestForEachLeafBindingVarID(t *testing.T) {
	v := NewIdentPat("v", false, nil, nil, emptySpan())
	v.VarID = 42
	pat := NewExtractorPat(NewIdentifier("Some", emptySpan()), []Pat{v}, emptySpan())

	got := map[string]int{}
	ForEachLeafBinding(pat, func(name string, varID int) { got[name] = varID })

	if got["v"] != 42 {
		t.Fatalf("leaf v VarID = %d, want 42", got["v"])
	}
}
