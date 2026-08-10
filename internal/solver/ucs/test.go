package ucs

import (
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
)

// RestKind records whether the source pattern wrote a rest element. `[first]` and
// `{x}` did not; `[first, ...rest]` and `{x, ...rest}` did.
//
// It is a marker on the structural tests rather than a test of its own, because a
// rest pattern contributes no tag. All it does is relax the shape the enclosing
// object or tuple pattern already names. The value a rest pattern binds is named
// elsewhere, by a SuffixStep or a RemainderStep on the projection path.
//
// The marker is a fact about the pattern's syntax, not a claim about what the test
// accepts, and the two do not line up. Leaf binding runs through bindPattern, whose
// object requirement is inexact for every object pattern: its propReq helper builds
// `{name: t, ...}`, "the receiver has at least this field". So a plain `{x, y}` arm
// already matches a value carrying extra fields, and NoRest does not mean the derived
// constraint rejects them. A consumer that wants extra fields rejected decides that
// for itself.
//
// Naming the syntax also keeps this apart from soltype's exactness, which is a real
// property of a type. There, ObjectType.Inexact and the `Exact<T>` and `Inexact<T>`
// intrinsics do govern what a value satisfies.
type RestKind int

const (
	// NoRest marks a pattern that named every element or field it matches.
	NoRest RestKind = iota
	// TrailingRest marks a pattern whose last element is a rest, so the fixed part
	// the pattern names is a prefix and anything past it is unconstrained. Only a
	// trailing rest is expressible; see SuffixStep for why an interior one is not.
	TrailingRest
)

// String renders the marker as the phrase used in a diagnostic.
func (r RestKind) String() string {
	switch r {
	case NoRest:
		return "no rest"
	case TrailingRest:
		return "trailing rest"
	default:
		return "unknown rest kind"
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
func (*AnnTest) isTest()       {}

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
	Keys []ObjectKey
	Rest RestKind
}

// TupleTest matches a structural tuple shape of Len elements. An element's
// sub-pattern is not part of the test. It becomes a split over the projected
// element. Under TrailingRest, Len counts only the fixed prefix a tuple rest pattern
// leaves, so `[first, ...rest]` has Len 1. See SuffixStep for why only a trailing
// rest is expressible.
type TupleTest struct {
	Len  int
	Rest RestKind
}

// LitTest matches a literal value, the `1` of `match n { 1 => … }`.
type LitTest struct{ Lit ast.Lit }

// ClassTest matches a nominal class tag, the `Point` of an instance pattern
// `Point { x, y }`. The pattern's fields are not part of the test; each becomes a
// projected sub-scrutinee, the same flattening a structural object gets.
type ClassTest struct{ Name ast.QualIdent }

// ExtractorTest matches an extractor tag, the `Ok` of `Ok(v)`. Arity is how many
// positional values the pattern takes from the extractor, each reached by an
// ExtractStep.
type ExtractorTest struct {
	Name  ast.QualIdent
	Arity int
}

// AnnTest matches a narrowing type annotation, the `number` of `if val x: number = u`. A
// value passes when it is one of the annotation's, so a `u` holding a `string` fails and
// takes the `else`. That is what makes an annotated binding refutable, where the same
// identifier without one matches every value.
//
// The tag comes from what the surface wrote rather than from a pattern's shape, so all
// three refutable forms produce it. The `match` arm `x: number => x` narrows exactly as
// `if val x: number = u` does. An annotation on a pattern's nested leaf, the `string` of
// `[a: string, b]`, mints no test. It asserts against the value that leaf binds.
type AnnTest struct{ Ann ast.TypeAnn }

// String renders the tag test.
func (t *ObjectTest) String() string    { return testString(t) }
func (t *TupleTest) String() string     { return testString(t) }
func (t *LitTest) String() string       { return testString(t) }
func (t *ClassTest) String() string     { return testString(t) }
func (t *ExtractorTest) String() string { return testString(t) }
func (t *AnnTest) String() string       { return testString(t) }

// testString renders a branch's tag test. A structural test relaxed by a rest
// pattern renders a trailing `...`, matching how an inexact type prints.
func testString(t Test) string {
	switch t := t.(type) {
	case nil:
		return "<nil>"
	case *ObjectTest:
		parts := make([]string, 0, len(t.Keys)+1)
		for _, key := range t.Keys {
			// A trailing `?` marks a key the test tolerates being absent, matching how
			// an optional property prints in a type.
			if key.Optional {
				parts = append(parts, key.Name+"?")
			} else {
				parts = append(parts, key.Name)
			}
		}
		if t.Rest == TrailingRest {
			parts = append(parts, "...")
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *TupleTest:
		parts := make([]string, 0, t.Len+1)
		for range t.Len {
			parts = append(parts, "_")
		}
		if t.Rest == TrailingRest {
			parts = append(parts, "...")
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *LitTest:
		return litString(t.Lit)
	case *ClassTest:
		return ast.QualIdentToString(t.Name)
	case *ExtractorTest:
		args := make([]string, t.Arity)
		for i := range args {
			args[i] = "_"
		}
		return ast.QualIdentToString(t.Name) + "(" + strings.Join(args, ", ") + ")"
	case *AnnTest:
		// The leading colon is the syntax the annotation is written with, so the test
		// reads as the tail of the binding it came from: `: number`.
		return ": " + annString(t.Ann)
	default:
		return nodeKind(t)
	}
}
