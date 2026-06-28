package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- M6 PR8: subsume inferred types at finalization ---

// subsumeFinal collapses a coalesced `number | 1` to `number`, since `1 <: number`.
// combine builds the union Context-free, so the redundant literal survives until
// the finalization pass re-mints it with the ambient Context.
func TestSubsumeFinalUnionLiteralIntoPrimitive(t *testing.T) {
	c := newChecker()
	in := newUnion(nil, parseTypes(t, "number", "1"), false)
	require.IsType(t, &soltype.UnionType{}, in, "precondition: combine leaves both members")
	got := c.subsumeFinal(in)
	require.True(t, equalType(parseType(t, "number"), got), "got %s", soltype.Print(got))
}

// The meet twin: `{x, ...} & {x, y, ...}` collapses to `{x, y, ...}`, since the
// wider object is a subtype of the narrower one under inexact width subtyping, so
// the narrower member already constrains the value.
func TestSubsumeFinalIntersectionObjects(t *testing.T) {
	c := newChecker()
	in := newIntersection(nil, parseTypes(t,
		"{x: number, ...}",
		"{x: number, y: string, ...}",
	))
	require.IsType(t, &soltype.IntersectionType{}, in, "precondition: combine leaves both members")
	got := c.subsumeFinal(in)
	require.True(t, equalType(parseType(t, "{x: number, y: string, ...}"), got), "got %s", soltype.Print(got))
}

// A member that still carries a free type variable is left in place. The pass is
// concrete-gated, so a scheme whose union is not yet ground is unchanged.
func TestSubsumeFinalLeavesFreeVar(t *testing.T) {
	c := newChecker()
	v := c.ctx.freshVar(0)
	got := c.subsumeFinal(newUnion(nil, []soltype.Type{parseType(t, "number"), v}, false))
	require.IsType(t, &soltype.UnionType{}, got, "got %s", soltype.Print(got))
	require.Len(t, got.(*soltype.UnionType).Types, 2)
}

// The walk re-mints bottom-up, so a union nested inside another type is subsumed
// too. A tuple `[number | 1]` finalizes to `[number]`.
func TestSubsumeFinalNestedUnion(t *testing.T) {
	c := newChecker()
	nested := newUnion(nil, parseTypes(t, "number", "1"), false)
	in := &soltype.TupleType{Elems: []soltype.Type{nested}}
	got := c.subsumeFinal(in)
	require.True(t, equalType(parseType(t, "[number]"), got), "got %s", soltype.Print(got))
}

// An inferred `1 | number` renders `number` end to end. The two return points are
// the literal `1` and the `number` param `n`, so the join is `1 | number`, which
// the generalization-time subsumption collapses to `number`.
func TestInferSubsumesUnionToPrimitive(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(b: boolean, n: number) {
			return if b { 1 } else { n }
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (b: boolean, n: number) -> number", values["f"])
}

// The finalized inferred type is equalType-equal to the annotated form, so caching
// and annotation round-trip agree. The body infers `1 | number`; the annotation
// resolves to `number`; after subsumption the two are equalType-equal.
func TestInferSubsumedTypeEqualsAnnotation(t *testing.T) {
	scope, _, errs := InferModule(parseModule(t, `
		fn inferred(b: boolean, n: number) {
			return if b { 1 } else { n }
		}
		val annotated: number = 0
	`))
	require.Empty(t, errs)

	inferredFn := schemeType(scope.values["inferred"].Schemes[0]).(*soltype.FuncType)
	annotated := schemeType(scope.values["annotated"].Schemes[0])
	require.True(t, equalType(annotated, inferredFn.Ret),
		"inferred return %s should equal annotated %s",
		soltype.Print(inferredFn.Ret), soltype.Print(annotated))
}

// Subsumption is display-only: the scheme's operative body keeps `1 | number`,
// so assignability is unchanged. A `number` sink accepts the value and a `string`
// sink rejects it, with the rejection decomposing the union for-all exactly as it
// would against the un-subsumed form.
func TestInferSubsumedTypePreservesAssignability(t *testing.T) {
	t.Run("number accepted", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn f(b: boolean, n: number) {
				val r = if b { 1 } else { n }
				val s: number = r
			}
		`)
		require.Empty(t, errs)
	})
	t.Run("string rejected", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn f(b: boolean, n: number) {
				val r = if b { 1 } else { n }
				val s: string = r
			}
		`)
		require.Equal(t, []string{
			"4:21-4:22: cannot constrain 1 <: string",
			"4:21-4:22: cannot constrain number <: string",
		}, messagesWithSpan(errs))
	})
}
