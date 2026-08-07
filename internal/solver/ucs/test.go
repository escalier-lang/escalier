package ucs

import "github.com/escalier-lang/escalier/internal/ast"

// Exactness records whether a structural test demands the exact shape its pattern
// names or accepts any value that has at least that shape. A rest pattern sets
// InexactPrefix. `[first, ...rest]` matches a tuple of at least one element and
// `{x, ...rest}` matches an object with at least an `x` field.
//
// Inexactness is a marker on the structural tests rather than a test of its own,
// because a rest pattern contributes no tag. It only relaxes the tag the enclosing
// object or tuple pattern already tests. The value a rest pattern binds is named
// elsewhere, by a SuffixStep or a RemainderStep on the projection path.
type Exactness int

const (
	// Exact matches only a value of precisely the shape the pattern names.
	Exact Exactness = iota
	// InexactPrefix matches any value that has at least the shape the pattern names.
	InexactPrefix
)

// String renders the exactness as the phrase used in a diagnostic.
func (e Exactness) String() string {
	switch e {
	case Exact:
		return "exact"
	case InexactPrefix:
		return "inexact prefix"
	default:
		return "unknown exactness"
	}
}

// Test is the tag one branch of a normalized split tests its scrutinee against. A
// test covers exactly one tag-level and never a nested shape. A sub-pattern becomes
// a projected sub-scrutinee with a split of its own, which is what makes the
// normalized form backtracking-free.
//
// A split is agnostic to which kind its test is, so structural shapes, literals,
// nominal class tags, and extractor tags all flow through one mechanism.
type Test interface{ isTest() }

func (*ObjectTest) isTest()    {}
func (*TupleTest) isTest()     {}
func (*LitTest) isTest()       {}
func (*ClassTest) isTest()     {}
func (*ExtractorTest) isTest() {}

// ObjectTest matches a structural object shape. Keys are the fields the pattern
// named at this level, in source order. A field's own sub-pattern is not part of the
// test; it becomes a split over the projected field.
type ObjectTest struct {
	Keys      []string
	Exactness Exactness
}

// TupleTest matches a structural tuple shape of Len elements. An element's
// sub-pattern is not part of the test; it becomes a split over the projected
// element. Under InexactPrefix, Len counts only the fixed prefix a tuple rest
// pattern leaves, so `[first, ...rest]` has Len 1.
type TupleTest struct {
	Len       int
	Exactness Exactness
}

// LitTest matches a literal value, the `1` of `match n { 1 => … }`.
type LitTest struct{ Lit ast.Lit }

// ClassTest matches a nominal class tag, the `Point` of an instance pattern
// `Point { x, y }`. The pattern's fields are not part of the test; each becomes a
// projected sub-scrutinee, the same flattening a structural object gets.
type ClassTest struct{ Name ast.QualIdent }

// ExtractorTest matches an extractor tag, the `Ok` of `Ok(v)`. Arity is how many
// positional results the pattern takes from the extractor, each reached by a
// ResultStep.
type ExtractorTest struct {
	Name  ast.QualIdent
	Arity int
}
