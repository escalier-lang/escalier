package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// M4 E3: a top-level destructuring `val [a, b] = …` binds SEVERAL names from one
// decl. The SCC driver fans the decl across its leaf binding keys, types the
// initializer once, and lowers each leaf's projected type into its pre-bound var.
// This replaces the former TestInferModuleDestructuringPatternUnsupported, which
// asserted the binding deferred with an UnsupportedNodeError.
func TestInferModuleTupleDestructuring(t *testing.T) {
	values, _, errs := inferSource(t, `val [a, b] = [1, 2]`)
	require.Empty(t, errs)
	require.Equal(t, "1", values["a"])
	require.Equal(t, "2", values["b"])
}

// A top-level `var` destructuring widens each leaf to its primitive, the
// constraint-level widening inferVarDeclInit applies to a direct literal
// initializer (M4 B3), so the leaves read back as `number` rather than the
// literal singletons.
func TestInferModuleTupleDestructuringVarWidens(t *testing.T) {
	values, _, errs := inferSource(t, `var [a, b] = [1, 2]`)
	require.Empty(t, errs)
	require.Equal(t, "number", values["a"])
	require.Equal(t, "number", values["b"])
}

// An object destructuring at module scope binds each leaf at its field type. The
// scrutinee is another top-level binding, so this also exercises the dep-graph
// ordering: `p` is inferred before the destructuring decl that reads it.
func TestInferModuleObjectDestructuring(t *testing.T) {
	values, _, errs := inferSource(t, `
		val p = {x: 5, y: 10}
		val {x, y} = p
	`)
	require.Empty(t, errs)
	require.Equal(t, "5", values["x"])
	require.Equal(t, "10", values["y"])
}

// A partial object destructuring binds only the named fields and tolerates the
// rest, since the pattern requirement is inexact. That requirement is the same
// width-tolerant member requirement a field read mints.
func TestInferModuleObjectDestructuringPartial(t *testing.T) {
	values, _, errs := inferSource(t, `
		val p = {x: 5, y: 10}
		val {x} = p
	`)
	require.Empty(t, errs)
	require.Equal(t, "5", values["x"])
	require.NotContains(t, values, "y")
}

// A field the scrutinee lacks is still rejected, surfacing MissingPropertyError —
// the binding side is the same constraint path body-level destructuring uses.
func TestInferModuleObjectDestructuringMissingField(t *testing.T) {
	_, _, errs := inferSource(t, `
		val p = {x: 5}
		val {x, z} = p
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "object is missing property: z", errs[0].Message())
}

// A wrong tuple arity is rejected with a TupleLengthMismatchError.
func TestInferModuleTupleDestructuringWrongArity(t *testing.T) {
	_, _, errs := inferSource(t, `val [a, b, c] = [1, 2]`)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot constrain tuple of length 2 <: tuple of length 3", errs[0].Message())
}

// A destructuring whose initializer recovered to the ErrorType sentinel recovers
// EACH leaf AS the sentinel rather than freezing it to `never`, so a single
// unknown-identifier diagnostic is reported and no leaf cascades `<: never`
// downstream.
func TestInferModuleDestructuringErrorRecovery(t *testing.T) {
	values, _, errs := inferSource(t, `val {x, y} = nope`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unknown identifier: nope", errs[0].Message())
	require.Equal(t, "error", values["x"])
	require.Equal(t, "error", values["y"])
}

// A nested pattern binds only its leaf identifiers. The leaf name the pattern walk
// collects must match the name dep_graph registered the key under, since a nested
// leaf is reached through the recursive bindPatternWith descent. `x` binds. The
// intermediate `p` is not a binding.
func TestInferModuleNestedObjectDestructuring(t *testing.T) {
	values, _, errs := inferSource(t, `val {p: {x}} = {p: {x: 5}}`)
	require.Empty(t, errs)
	require.Equal(t, "5", values["x"])
	require.NotContains(t, values, "p")
}

// A nested tuple pattern binds each leaf through the same recursive descent. `a`
// binds at the inner element, `b` at the outer.
func TestInferModuleNestedTupleDestructuring(t *testing.T) {
	values, _, errs := inferSource(t, `val [[a], b] = [[1], 2]`)
	require.Empty(t, errs)
	require.Equal(t, "1", values["a"])
	require.Equal(t, "2", values["b"])
}

// A destructured leaf is a real top-level binding another declaration can resolve.
// `z` reads `x`, so the two land in separate components and the driver must have
// bound `x` in scope before `z`'s component runs.
func TestInferModuleDestructuredLeafForwardReference(t *testing.T) {
	values, _, errs := inferSource(t, `
		val {x} = {x: 5}
		val z = x
	`)
	require.Empty(t, errs)
	require.Equal(t, "5", values["x"])
	require.Equal(t, "5", values["z"])
}

// Phase 3 generalizes each leaf key independently, so a destructured leaf bound to
// a polymorphic function generalizes to its own scheme and instantiates fresh at
// each call site. That is full let-polymorphism, not a shared monomorphic projection.
func TestInferModuleDestructuredLeafGeneralizes(t *testing.T) {
	values, _, errs := inferSource(t, `
		val {id} = {id: fn (x) { return x }}
		val a = id(1)
		val b = id("hi")
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(x: T0) -> T0", values["id"])
	require.Equal(t, "1", values["a"])
	require.Equal(t, `"hi"`, values["b"])
}

// An un-annotated `var` object destructuring widens each leaf to its primitive, the
// object twin of the tuple case (M4 B3), so a later reassignment of the same
// primitive checks.
func TestInferModuleObjectDestructuringVarWidens(t *testing.T) {
	values, _, errs := inferSource(t, `var {x} = {x: 5}`)
	require.Empty(t, errs)
	require.Equal(t, "number", values["x"])
}

// A genuinely recursive group threaded through a destructured binding resolves
// through the phase-1 pre-bound leaf vars: `f` calls `g`, and `g` is destructured
// from a record whose field is `f`, so `f` and `g` form one strongly connected
// component. The leaf binding var `g` is visible while `f`'s body is inferred, the
// same way a single recursive `val` resolves through its var.
func TestInferModuleDestructuringRecursiveGroup(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f() { return g() }
		val {g} = {g: f}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> never", values["f"])
	require.Equal(t, "fn () -> never", values["g"])
}
