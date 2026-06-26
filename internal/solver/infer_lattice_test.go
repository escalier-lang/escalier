package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- Union/intersection annotation input through resolveTypeAnn ---

func TestInferUnionAnnotationAcceptsMember(t *testing.T) {
	values, _, errs := inferSource(t, `val x: number | string = 5`)
	require.Empty(t, errs)
	require.Equal(t, "number | string", values["x"])
}

func TestInferUnionAnnotationAcceptsOtherMember(t *testing.T) {
	values, _, errs := inferSource(t, `val x: number | string = "hi"`)
	require.Empty(t, errs)
	require.Equal(t, "number | string", values["x"])
}

// `b` is a function param so the inferred sub is `boolean` rather than a
// `true`/`false` literal — otherwise the error message would not match.
func TestInferUnionAnnotationRejectsNonMember(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn check(b: boolean) {
			val x: number | string = b
		}
	`)
	require.Len(t, errs, 1)
	require.IsType(t, &CannotConstrainError{}, errs[0])
	require.Equal(t, "cannot constrain boolean <: number | string", errs[0].Message())
}

func TestInferUnionAnnotationRoundTrip(t *testing.T) {
	values, _, errs := inferSource(t, `val x: number | string | boolean = 5`)
	require.Empty(t, errs)
	require.Equal(t, "number | string | boolean", values["x"])
}

// Members are inexact so the literal's union of fields satisfies each side
// through width subtyping. Exact members would reject the cross-field.
func TestInferIntersectionAnnotationAcceptsValueAtBothMembers(t *testing.T) {
	values, _, errs := inferSource(t, `val r: {x: number, ...} & {y: string, ...} = {x: 1, y: "hi"}`)
	require.Empty(t, errs)
	require.Equal(t, "{x: number, ...} & {y: string, ...}", values["r"])
}

func TestInferIntersectionAnnotationRejectsMissingMember(t *testing.T) {
	_, _, errs := inferSource(t, `val r: {x: number, ...} & {y: string, ...} = {x: 1}`)
	require.Len(t, errs, 1)
	require.IsType(t, &MissingPropertyError{}, errs[0])
	require.Equal(t, "object is missing property: y", errs[0].Message())
}

func TestInferUnionAnnotationFlattens(t *testing.T) {
	values, _, errs := inferSource(t, `val x: (number | string) | boolean = 5`)
	require.Empty(t, errs)
	require.Equal(t, "number | string | boolean", values["x"])
}

// `number | number` exercises dedup, not subsumption proper — the
// literal-vs-primitive case `1 | number` needs LitTypeAnn support in
// resolveTypeAnn first.
func TestInferUnionAnnotationDedups(t *testing.T) {
	values, _, errs := inferSource(t, `val x: number | number = 5`)
	require.Equal(t, []string(nil), Messages(errs))
	require.Equal(t, "number", values["x"])
}

// Regression for the pre-switch placement of the union-super exists rule:
// without it the RefType arm in the structural switch would intercept and
// reject the borrow before the lattice rule could match the borrow member.
func TestInferUnionAnnotationBorrowMember(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn check(r: &mut {x: number}) {
			val v: &mut {x: number} | number = r
		}
	`)
	require.Equal(t, []string(nil), Messages(errs))
	require.Equal(t, "fn (r: &mut {x: number}) -> void", values["check"])
}
