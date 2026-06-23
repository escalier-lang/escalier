package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M6 PR2: union/intersection annotation input through resolveTypeAnn ---
//
// Source-level tests for the annotation path. They cover what the constrain
// lattice block accepts/rejects when the lattice node comes from a written
// type annotation, plus printer round-trip and Prov-cascade-safe recovery.

// A `number | string` annotation resolves to a UnionType and accepts an
// initializer that flows into ONE of the members.
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

// A `number | string` annotation rejects a value that is not a subtype of any
// member. The error names the whole union as the supertype. Use a function
// parameter as the source so the inferred sub is a primitive type, not a
// literal.
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

// A union annotation round-trips through the printer: the rendered binding
// type matches what the user wrote (modulo canonical member order, which
// matches declaration order for distinct primitives in this case).
func TestInferUnionAnnotationRoundTrip(t *testing.T) {
	values, _, errs := inferSource(t, `val x: number | string | boolean = 5`)
	require.Empty(t, errs)
	require.Equal(t, "number | string | boolean", values["x"])
}

// An intersection annotation accepts a value that is a subtype of EVERY
// member. The for-all rule decomposes per member. Use inexact members so a
// literal carrying fields from both sides satisfies each side through width
// subtyping — an exact `{x: number}` would reject the extra `y` field.
func TestInferIntersectionAnnotationAcceptsValueAtBothMembers(t *testing.T) {
	values, _, errs := inferSource(t, `val r: {x: number, ...} & {y: string, ...} = {x: 1, y: "hi"}`)
	require.Empty(t, errs)
	require.Equal(t, "{x: number, ...} & {y: string, ...}", values["r"])
}

// An intersection annotation rejects a value that misses a member's
// requirements. The for-all rule reports the failed branch.
func TestInferIntersectionAnnotationRejectsMissingMember(t *testing.T) {
	_, _, errs := inferSource(t, `val r: {x: number, ...} & {y: string, ...} = {x: 1}`)
	require.Len(t, errs, 1)
	require.IsType(t, &MissingPropertyError{}, errs[0])
	require.Equal(t, "object is missing property: y", errs[0].Message())
}

// A union annotation whose members are themselves unions flattens at the
// smart-constructor level — `(A | B) | C` renders as `A | B | C` rather
// than nesting.
func TestInferUnionAnnotationFlattens(t *testing.T) {
	values, _, errs := inferSource(t, `val x: (number | string) | boolean = 5`)
	require.Empty(t, errs)
	require.Equal(t, "number | string | boolean", values["x"])
}

// Subsumed-member elimination collapses a redundant member at the
// annotation site, since newUnion runs subsumption when handed the
// checker's Context. `number | number` dedups before subsumption runs;
// to exercise subsumption proper a literal-versus-primitive pair would
// be needed, but `ast.LitTypeAnn` isn't a resolveTypeAnn-supported node
// yet, so the dedup test stands in until literal annotations land.
func TestInferUnionAnnotationDedups(t *testing.T) {
	values, _, errs := inferSource(t, `val x: number | number = 5`)
	require.Equal(t, []string(nil), Messages(errs))
	require.Equal(t, "number", values["x"])
}

// A union of a borrow type and a value type — the lattice block must match
// the borrow against the borrow member before the structural switch's
// RefType arm intercepts and treats the union super as a concrete non-
// variable demand. Without the pre-switch placement, the RefType arm would
// see "super is not a variable" and peel the sub to a non-borrow, dropping
// its mutability and rejecting the assignment.
func TestInferUnionAnnotationBorrowMember(t *testing.T) {
	// A function-param borrow flows into a union member; the for-all rule
	// here would not save us — the SUPER is the union. Without the pre-
	// switch placement the RefType arm would see the union super and reject
	// the borrow at the "peel" step. The annotation member sets the type the
	// param is borrowed as, so the trial against the matching member succeeds
	// through ordinary RefType <: RefType.
	values, _, errs := inferSource(t, `
		fn check(r: &mut {x: number}) {
			val v: &mut {x: number} | number = r
		}
	`)
	require.Equal(t, []string(nil), Messages(errs))
	require.Equal(t, "fn (r: mut {x: number}) -> void", values["check"])
}
