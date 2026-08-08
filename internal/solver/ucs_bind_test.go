package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver/ucs"
	"github.com/stretchr/testify/require"
)

// UCS IR PR5: project-and-bind over an IR projection path. Every case here hands the
// binder a path the `ucs` package could have produced and checks the type its leaf binds
// at. Nothing calls the binder from the inference walk yet, so no case runs a `match`
// through it; that arrives with PR6.

// newPathChecker infers src into a fresh checker and returns it with the module scope, so
// a case can name a class the source declared. Pass an empty source when the root type is
// built by hand.
func newPathChecker(t *testing.T, src string) (*checker, *Scope) {
	t.Helper()
	c := newChecker()
	scope := sharedPrelude().Child()
	module := parseModule(t, src)
	c.inferDepGraph(scope, 0, module, dep_graph.BuildDepGraph(module))
	require.Empty(t, messagesWithSpan(c.errs), "the harness source must infer cleanly")
	return c, scope
}

// seedPath builds the root scrutinee for a target written `p` and the binder over it,
// the state a walk is in once it has inferred the match target. The identifier is also
// the node a constraint the resolution emits blames.
func seedPath(c *checker, rootType soltype.Type) (*ucs.Scrutinee, *pathBinder) {
	target := identExpr("p")
	root := ucs.NewRoot(target, ucs.At(ucs.OriginMatchArm, target))
	return root, c.newPathBinder(0, target, root, rootType)
}

// leafPat builds the plain identifier leaf a bind node names, `ast.NewIdentPat`'s
// no-annotation, no-default, immutable form.
func leafPat(name string) *ast.IdentPat {
	return ast.NewIdentPat(name, false, nil, nil, builderSpan())
}

// defaultedLeafPat builds the leaf a `{x = 0}` shorthand introduces: an identifier whose
// default supplies a value when the field is absent. An object test marks such a key
// optional, which is what relaxes the field lookup.
func defaultedLeafPat(name string) *ast.IdentPat {
	return ast.NewIdentPat(name, false, nil, numExpr(0), builderSpan())
}

// projectPath applies each step in turn from root, sharing one *ucs.Scrutinee per level
// the way normalization does, and returns the scrutinee the last step reaches.
func projectPath(root *ucs.Scrutinee, steps ...ucs.Step) *ucs.Scrutinee {
	s := root
	for _, step := range steps {
		s = s.Project(step, ucs.At(ucs.OriginMatchArm, root.Target))
	}
	return s
}

// boundType renders the type a leaf landed at in scope, coalesced the way a rendered
// binding is, so a case asserts Escalier annotation syntax rather than a raw variable.
func boundType(t *testing.T, c *checker, scope *Scope, name string) string {
	t.Helper()
	b, ok := scope.values[name]
	require.True(t, ok, "no binding for %q", name)
	require.Len(t, b.Schemes, 1)
	return c.renderValueBinding(b.Schemes[0])
}

// classType returns the handle of a class the harness source declared, which is the
// scrutinee type a `match` over an instance of that class starts from.
func classType(t *testing.T, c *checker, scope *Scope, name string) soltype.Type {
	t.Helper()
	ct, ok := c.instancePatClass(scope, name)
	require.True(t, ok, "no class named %q", name)
	return ct
}

// TestPathBinderProjectsAndBinds is the contract: given a hand-built path, the leaf binds
// at the type the same leaf nested inside one whole pattern would bind at. Each case
// seeds a root scrutinee, applies the tag test the split over it would apply, projects
// the path, and binds one identifier leaf off it.
func TestPathBinderProjectsAndBinds(t *testing.T) {
	// A borrow needs a lifetime to be a real reference rather than an owned-mutable cell,
	// so the borrowed cases mint one.
	lt := &soltype.LifetimeVar{ID: 0, Level: 0}

	tests := map[string]struct {
		// src declares whatever the case names, and is empty when the root type is built
		// by hand.
		src string
		// rootType is the type the walk inferred for the match target.
		rootType func(t *testing.T, c *checker, scope *Scope) soltype.Type
		// test is the tag test the split over the root applies, and nil when the case
		// projects with no split above it.
		test ucs.Test
		// steps is the projection path from the root down to the leaf's source.
		steps []ucs.Step
		// leaf is the name the bind node introduces.
		leaf string
		// want is the leaf's rendered type.
		want string
		// wantErrs is the full set of messages the resolution reports, span-prefixed.
		wantErrs []string
	}{
		// A field of a structural object resolves through the same inexact one-property
		// requirement a written `{x}` pattern emits.
		"Field": {
			rootType: func(t *testing.T, _ *checker, _ *Scope) soltype.Type {
				return parseType(t, "{x: number, y: string}")
			},
			test:  &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "x"}, {Name: "y"}}},
			steps: []ucs.Step{ucs.FieldStep{Name: "x"}},
			leaf:  "x",
			want:  "number",
		},
		// A field the scrutinee lacks fails the same requirement, so the path reports the
		// missing property rather than binding the leaf at a silent `never`.
		"FieldMissing": {
			rootType: func(t *testing.T, _ *checker, _ *Scope) soltype.Type {
				return parseType(t, "{x: number}")
			},
			test:     &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "z"}}},
			steps:    []ucs.Step{ucs.FieldStep{Name: "z"}},
			leaf:     "z",
			want:     "never",
			wantErrs: []string{"1:1-1:2: object is missing property: z"},
		},
		// An optional property reads as `T | undefined`, so the projection takes no upper
		// bound of `T`: the bound would reject the `undefined` half of what the field
		// actually holds. bindPatMode's shorthand arm, which every written `{x}` goes
		// through, adds no bound either.
		"OptionalField": {
			rootType: func(_ *testing.T, _ *checker, _ *Scope) soltype.Type {
				return exactObj(&soltype.PropertyElem{Name: "x", Type: num(), Optional: true})
			},
			test:  &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "x"}}},
			steps: []ucs.Step{ucs.FieldStep{Name: "x"}},
			leaf:  "x",
			want:  "number | undefined",
		},
		// A tuple index reads the element the tuple test's whole-tuple requirement lowered
		// into position 1.
		"TupleIndex": {
			rootType: func(t *testing.T, _ *checker, _ *Scope) soltype.Type {
				return parseType(t, "[number, string]")
			},
			test:  &ucs.TupleTest{Len: 2},
			steps: []ucs.Step{ucs.IndexStep{Index: 1}},
			leaf:  "b",
			want:  "string",
		},
		// The tuple test's requirement is exact without a rest, so a scrutinee of the wrong
		// arity is rejected at the test rather than at any one element. The rejected
		// requirement leaves each element variable unbounded, so the leaf recovers as
		// `never`.
		"TupleIndexWrongArity": {
			rootType: func(t *testing.T, _ *checker, _ *Scope) soltype.Type {
				return parseType(t, "[number, string]")
			},
			test:     &ucs.TupleTest{Len: 3},
			steps:    []ucs.Step{ucs.IndexStep{Index: 0}},
			leaf:     "a",
			want:     "never",
			wantErrs: []string{"1:1-1:2: cannot constrain tuple of length 2 <: tuple of length 3"},
		},
		// A trailing rest relaxes the test to a prefix and binds the suffix, which the path
		// names with a suffix step rather than with an index.
		"TupleSuffix": {
			rootType: func(t *testing.T, _ *checker, _ *Scope) soltype.Type {
				return parseType(t, "[number, string, boolean]")
			},
			test:  &ucs.TupleTest{Len: 1, Rest: ucs.TrailingRest},
			steps: []ucs.Step{ucs.SuffixStep{From: 1}},
			leaf:  "rest",
			want:  "[string, boolean]",
		},
		// An object rest binds the scrutinee minus the keys the pattern named, which the
		// path names with a remainder step carrying that key set.
		"ObjectRemainder": {
			rootType: func(t *testing.T, _ *checker, _ *Scope) soltype.Type {
				return parseType(t, "{x: number, y: string, z: boolean}")
			},
			test:  &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "x"}}, Rest: ucs.TrailingRest},
			steps: []ucs.Step{ucs.RemainderStep{Exclude: set.FromSlice([]string{"x"})}},
			leaf:  "rest",
			want:  "{y: string, z: boolean}",
		},
		// An extractor's positional value resolves through the constructor parameter it
		// yields, the interim protocol bindExtractorPat also binds through. Point's
		// synthesized constructor takes its fields, so value 0 is `x`.
		"ExtractorResult": {
			src:      `class Point { x: number, y: number }`,
			rootType: func(t *testing.T, c *checker, scope *Scope) soltype.Type { return classType(t, c, scope, "Point") },
			test:     &ucs.ExtractorTest{Name: ast.NewIdentifier("Point", builderSpan()), Arity: 2},
			steps:    []ucs.Step{ucs.ExtractStep{Index: 0}},
			leaf:     "x",
			want:     "number",
		},
		// A class test projects the instance member view, so a field step off it reads the
		// class's own member rather than a structural property.
		"ClassField": {
			src:      `class Point { x: number, y: number }`,
			rootType: func(t *testing.T, c *checker, scope *Scope) soltype.Type { return classType(t, c, scope, "Point") },
			test:     &ucs.ClassTest{Name: ast.NewIdentifier("Point", builderSpan())},
			steps:    []ucs.Step{ucs.FieldStep{Name: "y"}},
			leaf:     "y",
			want:     "number",
		},
		// A structural test over a union keeps only the members it can destructure, so the
		// field step reads `x` off the one member that has it. Without the narrowing the
		// lookup would run against both members and widen the leaf to include the absent
		// field's `undefined`; UnionUnnarrowed below pins that contrast.
		"UnionNarrowed": {
			rootType: func(t *testing.T, _ *checker, _ *Scope) soltype.Type {
				return parseType(t, "{x: number} | {y: string}")
			},
			test:  &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "x"}}},
			steps: []ucs.Step{ucs.FieldStep{Name: "x"}},
			leaf:  "x",
			want:  "number",
		},
		// The same path with no test above it: nothing narrowed the union, so the lookup
		// runs against both members and reads `x` off `{y: string}` as `undefined`. That
		// widened leaf is what the narrowing in UnionNarrowed prevents.
		"UnionUnnarrowed": {
			rootType: func(t *testing.T, _ *checker, _ *Scope) soltype.Type {
				return parseType(t, "{x: number} | {y: string}")
			},
			steps: []ucs.Step{ucs.FieldStep{Name: "x"}},
			leaf:  "x",
			want:  "number | undefined",
		},
		// A test that matches every member narrows nothing, since the narrowed union would
		// reproduce the original.
		"UnionMatchesEveryMember": {
			rootType: func(t *testing.T, _ *checker, _ *Scope) soltype.Type {
				return parseType(t, "{x: number} | {x: string}")
			},
			test:  &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "x"}}},
			steps: []ucs.Step{ucs.FieldStep{Name: "x"}},
			leaf:  "x",
			want:  "number | string",
		},
		// A tuple test narrows a union by arity, the same shape rule
		// patternMatchesMemberShape applies to a written tuple pattern.
		"UnionNarrowedByTupleArity": {
			rootType: func(t *testing.T, _ *checker, _ *Scope) soltype.Type {
				return parseType(t, "[number] | [string, boolean]")
			},
			test:  &ucs.TupleTest{Len: 2},
			steps: []ucs.Step{ucs.IndexStep{Index: 1}},
			leaf:  "b",
			want:  "boolean",
		},
		// A `&mut` scrutinee projects each leaf as a mutable borrow bounded by the
		// scrutinee's lifetime, and the mode reaches the leaf through the path rather than
		// through a wrapper on the projected type.
		"MutBorrowedScrutinee": {
			rootType: func(t *testing.T, _ *checker, _ *Scope) soltype.Type {
				return &soltype.RefType{Mut: true, Lt: lt, Inner: parseType(t, "{p: {x: number}}").(soltype.RefInner)}
			},
			test:  &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "p"}}},
			steps: []ucs.Step{ucs.FieldStep{Name: "p"}},
			leaf:  "p",
			want:  "&mut {x: number}",
		},
		// A `&` scrutinee projects a shared borrow the same way.
		"SharedBorrowedScrutinee": {
			rootType: func(t *testing.T, _ *checker, _ *Scope) soltype.Type {
				return &soltype.RefType{Lt: lt, Inner: parseType(t, "{p: {x: number}}").(soltype.RefInner)}
			},
			test:  &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "p"}}},
			steps: []ucs.Step{ucs.FieldStep{Name: "p"}},
			leaf:  "p",
			want:  "&{x: number}",
		},
		// The borrow propagates the whole way down a path, not just one level, following
		// the same match ergonomics a nested pattern does.
		"BorrowPropagatesThroughNestedPath": {
			rootType: func(t *testing.T, _ *checker, _ *Scope) soltype.Type {
				return &soltype.RefType{Mut: true, Lt: lt, Inner: parseType(t, "{a: {b: {x: number}}}").(soltype.RefInner)}
			},
			test:  &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "a"}}},
			steps: []ucs.Step{ucs.FieldStep{Name: "a"}, ucs.FieldStep{Name: "b"}},
			leaf:  "b",
			want:  "&mut {x: number}",
		},
		// A primitive leaf of a borrowed scrutinee is copied rather than borrowed, the same
		// gate applyBindMode puts on a written pattern's leaves.
		"BorrowedPrimitiveLeafIsCopied": {
			rootType: func(t *testing.T, _ *checker, _ *Scope) soltype.Type {
				return &soltype.RefType{Mut: true, Lt: lt, Inner: parseType(t, "{x: number}").(soltype.RefInner)}
			},
			test:  &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "x"}}},
			steps: []ucs.Step{ucs.FieldStep{Name: "x"}},
			leaf:  "x",
			want:  "number",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c, scope := newPathChecker(t, tt.src)
			root, binder := seedPath(c, tt.rootType(t, c, scope))
			if tt.test != nil {
				binder = binder.narrowedBy(scope, root, tt.test)
			}
			// The leaves of one branch bind in a child scope, so a name one branch binds is
			// invisible to the next, exactly as inferMatchArms scopes an arm.
			armScope := scope.Child()
			binder.bindAt(armScope, projectPath(root, tt.steps...), leafPat(tt.leaf))
			require.Equal(t, tt.wantErrs, messagesWithSpan(c.errs))
			require.Equal(t, tt.want, boundType(t, c, armScope, tt.leaf))
		})
	}
}

// A scrutinee is materialized once: an inner split and every leaf beneath it hold the
// same *ucs.Scrutinee pointer, so the binder projects it once and every consumer reads
// the same type. Re-projecting would mint a second variable and emit a second member
// lookup, which is what the codegen note in the plan rules out.
func TestPathBinderMaterializesEachScrutineeOnce(t *testing.T) {
	c, scope := newPathChecker(t, "")
	root, binder := seedPath(c, parseType(t, "{a: {x: number, y: string}}"))
	binder = binder.narrowedBy(scope, root, &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "a"}}})

	inner := projectPath(root, ucs.FieldStep{Name: "a"})
	first := binder.typeAt(scope, inner)
	second := binder.typeAt(scope, inner)
	require.Same(t, first, second)

	// Both leaves of the inner split project out of that one shared scrutinee.
	binder = binder.narrowedBy(scope, inner, &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "x"}, {Name: "y"}}})
	armScope := scope.Child()
	binder.bindAt(armScope, projectPath(inner, ucs.FieldStep{Name: "x"}), leafPat("x"))
	binder.bindAt(armScope, projectPath(inner, ucs.FieldStep{Name: "y"}), leafPat("y"))
	require.Empty(t, messagesWithSpan(c.errs))
	require.Equal(t, "number", boundType(t, c, armScope, "x"))
	require.Equal(t, "string", boundType(t, c, armScope, "y"))
}

// Sibling branches of one split share their scrutinee's projection. Applying a test per
// branch must not re-project the value the split tests, which would mint a second variable
// and emit a second member lookup for it.
func TestPathBinderSiblingBranchesShareOneProjection(t *testing.T) {
	c, scope := newPathChecker(t, "")
	// The root has no `a`, so resolving `p.a` reports a missing property. Counting that
	// report is how the test sees whether the projection ran once or once per branch.
	root, binder := seedPath(c, parseType(t, "{b: number}"))
	binder = binder.narrowedBy(scope, root, &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "a"}}})
	inner := projectPath(root, ucs.FieldStep{Name: "a"})

	first := binder.narrowedBy(scope, inner, &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "x"}}})
	second := binder.narrowedBy(scope, inner, &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "y"}}})

	require.Equal(t, []string{"1:1-1:2: object is missing property: a"}, messagesWithSpan(c.errs))
	// Neither test narrows a non-union scrutinee, so both branches still hold the one
	// projection variable the shared resolution minted.
	require.Same(t, first.typeAt(scope, inner), second.typeAt(scope, inner))
}

// An index step with no tuple test above it mints the whole-tuple requirement itself and
// writes the shape back onto the scrutinee, so a sibling step off the same value reads
// that shape instead of minting a second requirement.
func TestPathBinderUntestedTupleIndexMintsOneRequirement(t *testing.T) {
	c, scope := newPathChecker(t, "")
	// The root is not a tuple, so the requirement an index step mints fails. Counting that
	// report is how the test sees how many requirements were minted.
	root, binder := seedPath(c, parseType(t, "{x: number}"))
	armScope := scope.Child()
	binder.bindAt(armScope, projectPath(root, ucs.IndexStep{Index: 1}), leafPat("b"))
	binder.bindAt(armScope, projectPath(root, ucs.IndexStep{Index: 0}), leafPat("a"))
	require.Equal(t, []string{
		"1:1-1:2: cannot constrain object <: tuple",
	}, messagesWithSpan(c.errs))
}

// Applying a test returns a derived binder rather than mutating the one it came from, so
// two branches of the same split narrow the same scrutinee to different union members and
// neither sees the other's view.
func TestPathBinderBranchesNarrowIndependently(t *testing.T) {
	c, scope := newPathChecker(t, "")
	root, binder := seedPath(c, parseType(t, "{x: number} | {y: string}"))

	first := binder.narrowedBy(scope, root, &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "x"}}})
	second := binder.narrowedBy(scope, root, &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "y"}}})

	firstScope := scope.Child()
	first.bindAt(firstScope, projectPath(root, ucs.FieldStep{Name: "x"}), leafPat("x"))
	secondScope := scope.Child()
	second.bindAt(secondScope, projectPath(root, ucs.FieldStep{Name: "y"}), leafPat("y"))

	require.Empty(t, messagesWithSpan(c.errs))
	require.Equal(t, "number", boundType(t, c, firstScope, "x"))
	require.Equal(t, "string", boundType(t, c, secondScope, "y"))
}

// A destructuring default replaces the `undefined` an optional property reads as, so
// `{x = 0}` over `{x?: number}` binds the property type joined with the default's, where
// the same field with no default binds `number | undefined`. The OptionalField case above
// pins the undefaulted half. This is the meaningful use of the marker an object test
// carries, and binding the same leaf through bindPattern renders the same `number | 0`.
func TestPathBinderDefaultFillsOptionalProperty(t *testing.T) {
	c, scope := newPathChecker(t, "")
	root, binder := seedPath(c, exactObj(&soltype.PropertyElem{Name: "x", Type: num(), Optional: true}))
	binder = binder.narrowedBy(scope, root, &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "x", Optional: true}}})

	armScope := scope.Child()
	binder.bindAt(armScope, projectPath(root, ucs.FieldStep{Name: "x"}), defaultedLeafPat("x"))
	require.Empty(t, messagesWithSpan(c.errs))
	require.Equal(t, "number | 0", boundType(t, c, armScope, "x"))
}

// A scrutinee that cannot carry the field at all still binds the default rather than
// reporting a missing property, because the marker relaxes the lookup to an optional
// requirement. This is the one scrutinee shape the marker is observable on: a required
// requirement already succeeds against an optional property and against a union whose
// members disagree, so neither distinguishes it.
//
// Whether `{x = 0}` should match a scrutinee with no `x` at all is a question about
// bindPattern rather than about the IR, and #1053 argues it should not.
// TestInferObjectPatternLeafDefault in infer_pattern_test.go pins the same answer for
// `val {z = 0} = p` over `{x: number}`, and this case exists to hold the path binder to
// it. Change the two together or not at all.
func TestPathBinderDefaultedKeyBindsAgainstScrutineeWithoutTheField(t *testing.T) {
	c, scope := newPathChecker(t, "")
	root, binder := seedPath(c, parseType(t, "{y: string}"))
	binder = binder.narrowedBy(scope, root, &ucs.ObjectTest{Keys: []ucs.ObjectKey{{Name: "x", Optional: true}}})

	armScope := scope.Child()
	binder.bindAt(armScope, projectPath(root, ucs.FieldStep{Name: "x"}), defaultedLeafPat("x"))
	require.Empty(t, messagesWithSpan(c.errs))
	require.Equal(t, "0", boundType(t, c, armScope, "x"))
}

// A test whose name resolves to no class or constructor reports against the name it
// wrote, and the leaves beneath it still bind so a later reference does not cascade into
// an unknown-identifier error.
func TestPathBinderUnresolvedTagNames(t *testing.T) {
	tests := map[string]struct {
		test ucs.Test
		step ucs.Step
		want string
	}{
		"NotAClass": {
			test: &ucs.ClassTest{Name: ast.NewIdentifier("Missing", builderSpan())},
			step: ucs.FieldStep{Name: "x"},
			want: "1:1-1:2: `Missing` does not name a class and cannot be used as an instance pattern.",
		},
		"NotAConstructor": {
			test: &ucs.ExtractorTest{Name: ast.NewIdentifier("Missing", builderSpan()), Arity: 1},
			step: ucs.ExtractStep{Index: 0},
			want: "1:1-1:2: `Missing` is not a constructor and cannot be used as an extractor pattern.",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c, scope := newPathChecker(t, "")
			root, binder := seedPath(c, parseType(t, "{x: number}"))
			binder = binder.narrowedBy(scope, root, tt.test)
			armScope := scope.Child()
			binder.bindAt(armScope, projectPath(root, tt.step), leafPat("x"))
			require.Equal(t, []string{tt.want}, messagesWithSpan(c.errs))
			require.Contains(t, armScope.values, "x")
		})
	}
}

// An extractor test that takes a different number of values than the constructor yields
// reports the arity mismatch once, at the test, rather than once per extract step.
func TestPathBinderExtractorArityMismatch(t *testing.T) {
	c, scope := newPathChecker(t, `class Point { x: number, y: number }`)
	root, binder := seedPath(c, classType(t, c, scope, "Point"))
	binder = binder.narrowedBy(scope, root, &ucs.ExtractorTest{Name: ast.NewIdentifier("Point", builderSpan()), Arity: 3})

	armScope := scope.Child()
	binder.bindAt(armScope, projectPath(root, ucs.ExtractStep{Index: 0}), leafPat("a"))
	binder.bindAt(armScope, projectPath(root, ucs.ExtractStep{Index: 2}), leafPat("c"))
	require.Equal(t, []string{
		"1:1-1:2: extractor pattern `Point` expects 2 arguments but got 3",
	}, messagesWithSpan(c.errs))
	// Value 0 still resolves through the constructor; value 2, which the constructor does
	// not yield, recovers against a fresh variable rather than a second diagnostic.
	require.Equal(t, "number", boundType(t, c, armScope, "a"))
	require.Contains(t, armScope.values, "c")
}

// A literal test asserts its literal is an admissible value of the scrutinee, the same
// direction bindPatMode's literal arm constrains in, and binds nothing itself.
func TestPathBinderLiteralTest(t *testing.T) {
	tests := map[string]struct {
		rootType string
		lit      ast.Lit
		wantErrs []string
	}{
		"Admissible":   {rootType: "number", lit: ast.NewNumber(1, builderSpan())},
		"WrongKind":    {rootType: "number", lit: ast.NewString("one", builderSpan()), wantErrs: []string{`1:1-1:2: cannot constrain "one" <: number`}},
		"NarrowerType": {rootType: "1 | 2", lit: ast.NewNumber(1, builderSpan())},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c, scope := newPathChecker(t, "")
			root, binder := seedPath(c, parseType(t, tt.rootType))
			binder.narrowedBy(scope, root, &ucs.LitTest{Lit: tt.lit})
			require.Equal(t, tt.wantErrs, messagesWithSpan(c.errs))
		})
	}
}
