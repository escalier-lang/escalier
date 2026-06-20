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
//
// Each case lists the names that must bind to a given rendered type (values), the
// names that must NOT bind (absent), and the full error messages expected in order
// (errs). A success case leaves errs empty.
func TestInferModuleDestructuring(t *testing.T) {
	type testCase struct {
		name   string
		src    string
		values map[string]string
		absent []string
		errs   []string
	}

	cases := []testCase{
		{
			// A tuple destructuring binds each leaf at its element type.
			name:   "tuple",
			src:    `val [a, b] = [1, 2]`,
			values: map[string]string{"a": "1", "b": "2"},
		},
		{
			// A `var` tuple destructuring widens each leaf to its primitive, the
			// constraint-level widening inferVarDeclInit applies to a direct literal
			// initializer (M4 B3), so the leaves read back as `number`.
			name:   "tuple var widens",
			src:    `var [a, b] = [1, 2]`,
			values: map[string]string{"a": "number", "b": "number"},
		},
		{
			// An object destructuring binds each leaf at its field type. The scrutinee
			// is another top-level binding, so this also exercises the dep-graph
			// ordering: `p` is inferred before the destructuring decl that reads it.
			name: "object",
			src: `
				val p = {x: 5, y: 10}
				val {x, y} = p
			`,
			values: map[string]string{"x": "5", "y": "10"},
		},
		{
			// A partial object destructuring binds only the named fields and tolerates
			// the rest, since the pattern requirement is inexact. That requirement is
			// the same width-tolerant member requirement a field read mints.
			name: "object partial",
			src: `
				val p = {x: 5, y: 10}
				val {x} = p
			`,
			values: map[string]string{"x": "5"},
			absent: []string{"y"},
		},
		{
			// A nested pattern binds only its leaf identifiers. The leaf name the
			// pattern walk collects must match the name dep_graph registered the key
			// under, since a nested leaf is reached through the recursive
			// bindPatternWith descent. `x` binds. The intermediate `p` is not a binding.
			name:   "nested object",
			src:    `val {p: {x}} = {p: {x: 5}}`,
			values: map[string]string{"x": "5"},
			absent: []string{"p"},
		},
		{
			// A nested tuple pattern binds each leaf through the same recursive descent.
			// `a` binds at the inner element, `b` at the outer.
			name:   "nested tuple",
			src:    `val [[a], b] = [[1], 2]`,
			values: map[string]string{"a": "1", "b": "2"},
		},
		{
			// A destructured leaf is a real top-level binding another declaration can
			// resolve. `z` reads `x`, so the two land in separate components and the
			// driver must have bound `x` in scope before `z`'s component runs.
			name: "forward reference to a leaf",
			src: `
				val {x} = {x: 5}
				val z = x
			`,
			values: map[string]string{"x": "5", "z": "5"},
		},
		{
			// Phase 3 generalizes each leaf key independently, so a destructured leaf
			// bound to a polymorphic function generalizes to its own scheme and
			// instantiates fresh at each call site. That is full let-polymorphism, not a
			// shared monomorphic projection.
			name: "leaf generalizes",
			src: `
				val {id} = {id: fn (x) { return x }}
				val a = id(1)
				val b = id("hi")
			`,
			values: map[string]string{"id": "fn <T0>(x: T0) -> T0", "a": "1", "b": `"hi"`},
		},
		{
			// An un-annotated `var` object destructuring widens each leaf to its
			// primitive, the object twin of the tuple case (M4 B3), so a later
			// reassignment of the same primitive checks.
			name:   "object var widens",
			src:    `var {x} = {x: 5}`,
			values: map[string]string{"x": "number"},
		},
		{
			// A genuinely recursive group threaded through a destructured binding
			// resolves through the phase-1 pre-bound leaf vars: `f` calls `g`, and `g`
			// is destructured from a record whose field is `f`, so `f` and `g` form one
			// strongly connected component. The leaf binding var `g` is visible while
			// `f`'s body is inferred, the same way a single recursive `val` resolves.
			name: "recursive group",
			src: `
				fn f() { return g() }
				val {g} = {g: f}
			`,
			values: map[string]string{"f": "fn () -> never", "g": "fn () -> never"},
		},
		{
			// A field the scrutinee lacks is rejected, surfacing MissingPropertyError —
			// the binding side is the same constraint path body-level destructuring uses.
			name: "missing field",
			src: `
				val p = {x: 5}
				val {x, z} = p
			`,
			errs: []string{"object is missing property: z"},
		},
		{
			// A wrong tuple arity is rejected with a TupleLengthMismatchError.
			name: "tuple wrong arity",
			src:  `val [a, b, c] = [1, 2]`,
			errs: []string{"cannot constrain tuple of length 2 <: tuple of length 3"},
		},
		{
			// An initializer that recovered to the ErrorType sentinel recovers EACH leaf
			// AS the sentinel rather than freezing it to `never`, so a single
			// unknown-identifier diagnostic is reported and no leaf cascades `<: never`.
			name:   "error recovery",
			src:    `val {x, y} = nope`,
			values: map[string]string{"x": "error", "y": "error"},
			errs:   []string{"Unknown identifier: nope"},
		},
		{
			// A destructured leaf colliding with another top-level binding of the same
			// name is a duplicate declaration, not an unsupported pattern. The first
			// binding is kept, the non-colliding leaf `y` still binds, and the collision
			// reports cleanly.
			name: "leaf name collides with another decl",
			src: `
				val {x, y} = {x: 1, y: 2}
				val x = 5
			`,
			values: map[string]string{"x": "1", "y": "2"},
			errs:   []string{"Duplicate declaration: x"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Len(t, errs, len(tc.errs))
			for i := range tc.errs {
				require.Equal(t, tc.errs[i], errs[i].Message())
			}
			for name, want := range tc.values {
				require.Equal(t, want, values[name], "value of %s", name)
			}
			for _, name := range tc.absent {
				require.NotContains(t, values, name)
			}
		})
	}
}
