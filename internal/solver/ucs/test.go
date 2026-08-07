package ucs

import "github.com/escalier-lang/escalier/internal/ast"

// Exactness records whether a rest pattern relaxed a structural test. `[first]` and
// `{x}` name a fixed shape, while `[first, ...rest]` and `{x, ...rest}` name only a
// prefix of one.
//
// It is a marker on the structural tests rather than a test of its own, because a
// rest pattern contributes no tag. All it does is relax the shape the enclosing
// object or tuple pattern already names. The value a rest pattern binds is named
// elsewhere, by a SuffixStep or a RemainderStep on the projection path.
//
// Exactness describes the pattern, not the constraint the solver derives from it.
// Leaf binding runs through bindPattern, whose object requirement is inexact for
// every object pattern. Its propReq helper builds `{name: t, ...}`, "the receiver has
// at least this field", so a plain `{x, y}` arm already matches a value carrying
// extra fields. A consumer that wants an Exact test to reject those extra fields
// decides that for itself.
type Exactness int

const (
	// Exact is the shape a pattern with no rest element names.
	Exact Exactness = iota
	// InexactPrefix is the relaxed shape a rest element leaves: at least the fixed
	// prefix the pattern names, with anything past it unconstrained.
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

// ObjectKey is one field an object test names.
type ObjectKey struct {
	Name string
	// Optional marks a field the pattern matches without, which a destructuring
	// default produces. `{x = 0}` binds `x` to 0 when the field is absent, so a test
	// that demanded `x` would send the value to the wrong branch. bindPattern already
	// passes `e.Default != nil` through to propReq for exactly this reason.
	Optional bool
}

// ObjectTest matches a structural object shape. Keys are the fields the pattern
// named at this level, in source order. A field's own sub-pattern is not part of the
// test. It becomes a split over the projected field.
type ObjectTest struct {
	Keys      []ObjectKey
	Exactness Exactness
}

// TupleTest matches a structural tuple shape of Len elements. An element's
// sub-pattern is not part of the test. It becomes a split over the projected
// element. Under InexactPrefix, Len counts only the fixed prefix a tuple rest pattern
// leaves, so `[first, ...rest]` has Len 1. See SuffixStep for why only a trailing
// rest is expressible.
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
