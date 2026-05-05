package tests

import (
	"context"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLifetimeUnificationByInference covers the observable end-to-end
// behavior of Phase 9.1–9.5: lifetime variables on generic-function
// signatures get instantiated at each call site, the unification engine
// binds them to the caller's argument lifetimes, and the resulting
// types print with the expected lifetime annotations. Each subtest
// type-checks a small script and asserts on either inferred binding
// types or the mut-transition errors produced when an inferred alias
// relationship is later violated.
func TestLifetimeUnificationByInference(t *testing.T) {
	t.Parallel()

	// Each case type-checks a small script and asserts the inferred
	// signatures and the formatted MutabilityTransitionError messages.
	// The phase notes on each case explain *why* the assertion shape is
	// what it is — see the per-case `note` field.
	cases := []struct {
		name string
		// note documents the phase under test and the reasoning behind
		// the expected outcome; included as the test failure message
		// for `mutErrors` so a regression points at the rationale, not
		// just the diff.
		note          string
		script        string
		expectedTypes map[string]string
		expectedErrs  []string
	}{
		{
			// 9.1 + 9.2: a generic identity returns a value sharing the
			// argument's alias set. Mutating the result while the argument
			// is frozen must produce the canonical transition error — the
			// only way that's possible is if the call site bound the
			// function's LifetimeVar to p's alias set via unification. We
			// also check that `identity`'s inferred signature still carries
			// the `<'a>` form after Phase 9.2's instantiation rewrites the
			// original LifetimeVars on each call.
			name: "GenericIdentityPropagatesLifetimeToResult",
			note: "phase 9.1+9.2: generic identity propagates 'a from arg to result",
			script: `
				fn identity(p: mut {x: number}) -> mut {x: number} { return p }
				val p: mut {x: number} = {x: 0}
				val r: mut {x: number} = identity(p)
				val q: {x: number} = p
				r.x = 5
				q
			`,
			expectedTypes: map[string]string{
				"identity": "fn <'a>(p: mut 'a {x: number}) -> mut 'a {x: number}",
			},
			expectedErrs: []string{
				"cannot assign 'p' to immutable 'q': 'r' still has mutable access to 'p' after this point",
			},
		},
		{
			// 9.2: the second call's fresh lifetime variables must be
			// independent of the first's. If `instantiateGenericFunc`
			// failed to freshen LifetimeVars, the second call's binding
			// would also pin `'a` against p, and mutating r2 would trigger
			// a spurious error on frozenP.
			name: "EachCallSiteGetsIndependentLifetimeVars",
			note: "phase 9.2: the second call's binding must not affect p's alias set",
			script: `
				fn identity(p: mut {x: number}) -> mut {x: number} { return p }
				val p: mut {x: number} = {x: 0}
				val q: mut {x: number} = {x: 1}
				val r1: mut {x: number} = identity(p)
				val r2: mut {x: number} = identity(q)
				val frozenP: {x: number} = p
				r2.x = 5
				frozenP
			`,
			expectedErrs: nil,
		},
		{
			// 9.5 — first §9.5 example: a higher-order function whose
			// callback shares a lifetime variable with the surrounding
			// signature must structurally unify. The user-written `'a` on
			// `apply` ties the callback's input/output and `apply`'s
			// argument together so the result aliases p.
			name: "HigherOrderCallbackUnifies",
			note: "phase 9.5: HOF callback shares 'a with surrounding signature",
			script: `
				type Point = {x: number}
				fn identity(p: mut Point) -> mut Point { return p }
				fn apply<'a>(f: fn(arg: mut 'a Point) -> mut 'a Point, p: mut 'a Point) -> mut 'a Point {
					return f(p)
				}
				val p: mut Point = {x: 0}
				val r: mut Point = apply(identity, p)
				r.x = 5
				r
			`,
			expectedTypes: map[string]string{
				"identity": "fn <'a>(p: mut 'a Point) -> mut 'a Point",
				"apply":    "fn <'a>(f: fn (arg: mut 'a Point) -> mut 'a Point, p: mut 'a Point) -> mut 'a Point",
			},
			expectedErrs: nil,
		},
		{
			// 9.5 — second §9.5 example: a higher-order function whose
			// callback has no shared lifetime with the surrounding
			// signature produces an independent result. Here `transform`
			// carries `<'a>` only on `p`; the callback `f`'s parameter and
			// return are unparameterized, so unification of `identity`
			// against `f` does not link `'a` to the callback's lifetimes —
			// the result of `transform(identity, p)` does not alias p.
			name: "HigherOrderCallbackWithoutSharedLifetime",
			note: "phase 9.5: HOF callback without shared 'a — result does not alias p",
			script: `
				type Point = {x: number}
				fn identity(p: mut Point) -> mut Point { return p }
				fn transform<'a>(f: fn(arg: mut Point) -> mut Point, p: mut 'a Point) -> mut Point {
					return f(p)
				}
				val p: mut Point = {x: 0}
				val r: mut Point = transform(identity, p)
				r.x = 5
				r
			`,
			expectedTypes: map[string]string{
				"transform": "fn <'a>(f: fn (arg: mut Point) -> mut Point, p: mut 'a Point) -> mut Point",
			},
			expectedErrs: nil,
		},
		{
			// 9.3: an argument passed to a `'static`-inferring parameter
			// is marked permanently aliased. Confirms that unification
			// doesn't reject `'static`-vs-non-static and that the
			// caller-side escape propagation still fires after Phase 9
			// wired up the unification hook in unifyInner. We also assert
			// that `store`'s inferred signature pins the parameter to
			// `'static`.
			name: "StaticParameterAbsorbsConcreteArgument",
			note: "phase 9.3: 'static-inferring param absorbs the arg's alias set",
			script: `
				var cache: mut {x: number} = {x: 0}
				fn store(p: mut {x: number}) -> number {
					cache = p
					return p.x
				}
				val p: mut {x: number} = {x: 0}
				store(p)
				val frozen: {x: number} = p
				frozen
			`,
			expectedTypes: map[string]string{
				"store": "fn (p: mut 'static {x: number}) -> number",
			},
			expectedErrs: []string{
				"cannot assign 'p' to immutable 'frozen': a `'static` escape still has mutable access to 'p' after this point",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			types, mutErrors := mustInferScript(t, tc.script)
			for name, want := range tc.expectedTypes {
				assert.Equal(t, want, types[name],
					"inferred signature for %q (%s)", name, tc.note)
			}
			if tc.expectedErrs == nil {
				assert.Empty(t, mutErrors, tc.note)
			} else {
				assert.Equal(t, tc.expectedErrs, mutErrors, tc.note)
			}
		})
	}
}

// TestUnifyLifetimesUnit covers the parts of the unifyLifetimes table
// that have no script equivalent, because the relevant LifetimeValues
// are only constructed via internal mechanisms (the alias tracker
// today does not attach LifetimeValues to ordinary call arguments —
// only `'static` is reachable from source). These cases pin the
// internal contract directly.
func TestUnifyLifetimesUnit(t *testing.T) {
	// Two missing lifetimes is degenerate input: no script can produce
	// a `unifyLifetimes(nil, nil)` call because the unifyInner hook
	// short-circuits before invoking the helper when both sides have
	// no lifetime. Asserted directly so the contract is explicit.
	t.Run("nil_pair_succeeds", func(t *testing.T) {
		c := NewChecker(context.Background())
		assert.Empty(t, c.UnifyLifetimes(Context{}, nil, nil))
	})

	// Free-var-to-value binding is what happens internally during
	// every `identity(p)` style call, but the binding is then pruned
	// away by the time the caller observes a result type — there's no
	// source-visible artifact that confirms `lv.Instance` was set
	// versus reset to nil. Asserted directly.
	t.Run("free_var_binds_to_value", func(t *testing.T) {
		c := NewChecker(context.Background())
		lv := c.FreshLifetimeVar("a")
		val := &type_system.LifetimeValue{ID: 7, Name: "p"}
		require.Empty(t, c.UnifyLifetimes(Context{}, lv, val))
		assert.Equal(t, type_system.Lifetime(val), lv.Instance)
	})

	// Var-to-var equating is exercised end-to-end by
	// HigherOrderCallbackUnifies, but that script can't observe
	// *which* var was chosen as the canonical one or whether pruning
	// follows the chain — only that the call type-checks. The unit
	// test verifies the chain is walkable in both directions.
	t.Run("var_to_var_links_them", func(t *testing.T) {
		c := NewChecker(context.Background())
		lv1 := c.FreshLifetimeVar("a")
		lv2 := c.FreshLifetimeVar("b")
		require.Empty(t, c.UnifyLifetimes(Context{}, lv1, lv2))
		val := &type_system.LifetimeValue{ID: 9, Name: "v"}
		require.Empty(t, c.UnifyLifetimes(Context{}, lv2, val))
		assert.Equal(t, type_system.Lifetime(val), type_system.PruneLifetime(lv1))
		assert.Equal(t, type_system.Lifetime(val), type_system.PruneLifetime(lv2))
	})

	// Distinct-value conflict (e.g. `swap<'a>(p, q)` where p and q are
	// independent values) is unreachable from a script today: ordinary
	// call arguments aren't tagged with LifetimeValues — only `'static`
	// escapes are — so the only reachable conflict shape is `'static`
	// vs `'static`, which the rules deliberately succeed on. Asserted
	// directly until the alias tracker starts emitting LifetimeValues
	// for non-static call arguments.
	t.Run("distinct_values_conflict", func(t *testing.T) {
		c := NewChecker(context.Background())
		v1 := &type_system.LifetimeValue{ID: 1, Name: "p"}
		v2 := &type_system.LifetimeValue{ID: 2, Name: "q"}
		errs := c.UnifyLifetimes(Context{}, v1, v2)
		require.Len(t, errs, 1)
		_, isMismatch := errs[0].(LifetimeMismatchError)
		assert.True(t, isMismatch)
	})

	// Static-vs-concrete is partially observable via
	// StaticParameterAbsorbsConcreteArgument, but only in one
	// direction (caller passes a non-static, callee declares
	// `'static`). The reverse direction can't be expressed in source —
	// there's no syntax for a caller to construct a `'static` value
	// and pass it to a non-static parameter. Asserted directly to pin
	// the symmetry.
	t.Run("static_absorbs_concrete", func(t *testing.T) {
		c := NewChecker(context.Background())
		stat := &type_system.LifetimeValue{ID: 1, Name: "static", IsStatic: true}
		v := &type_system.LifetimeValue{ID: 2, Name: "p"}
		assert.Empty(t, c.UnifyLifetimes(Context{}, stat, v))
		assert.Empty(t, c.UnifyLifetimes(Context{}, v, stat))
	})

	// A 3-deep chain (var → var → var → value) cannot arise from a
	// single call site — each `instantiateGenericFunc` invocation
	// creates fresh vars and resolves them in one unification round.
	// Multi-hop chains only form when higher-order callbacks repeatedly
	// link vars through `apply(apply(...))` style nesting, and even
	// then the printed signatures don't expose the chain depth. The
	// unit test confirms PruneLifetime walks an arbitrary chain length.
	t.Run("var_chain_pruned_to_value", func(t *testing.T) {
		c := NewChecker(context.Background())
		lv1 := c.FreshLifetimeVar("a")
		lv2 := c.FreshLifetimeVar("b")
		lv3 := c.FreshLifetimeVar("c")
		val := &type_system.LifetimeValue{ID: 5, Name: "p"}
		require.Empty(t, c.UnifyLifetimes(Context{}, lv1, lv2))
		require.Empty(t, c.UnifyLifetimes(Context{}, lv2, lv3))
		require.Empty(t, c.UnifyLifetimes(Context{}, lv3, val))
		assert.Equal(t, type_system.Lifetime(val), type_system.PruneLifetime(lv1))
	})

	// Pinned but skipped: distribution over a LifetimeUnion against a
	// free LifetimeVar produces a spurious LifetimeMismatchError today.
	// The first member binds the var; the second iteration then
	// unifies the second member against the already-bound var (now
	// pruned to the first member's value), reporting a mismatch
	// between two distinct values. The intended semantics is that the
	// var should be bound to the union as a whole.
	//
	// Not source-reachable yet (LifetimeUnion arises only on
	// multi-source returns and the alias tracker doesn't emit free
	// Vars on the rhs in that context), so this is left as a pending
	// fix tracked via the TODO in unify_lifetimes.go's
	// LifetimeUnion-distribution branch. Once Phase 10/11 makes the
	// shape reachable, remove the t.Skip and fix the distribution
	// rule.
	t.Run("union_vs_free_var_currently_misfires", func(t *testing.T) {
		t.Skip("pending fix: see TODO(phase-10/11) in unify_lifetimes.go")
		c := NewChecker(context.Background())
		v1 := &type_system.LifetimeValue{ID: 1, Name: "p"}
		v2 := &type_system.LifetimeValue{ID: 2, Name: "q"}
		union := &type_system.LifetimeUnion{
			Lifetimes: []type_system.Lifetime{v1, v2},
		}
		freeVar := c.FreshLifetimeVar("a")
		errs := c.UnifyLifetimes(Context{}, union, freeVar)
		assert.Empty(t, errs,
			"a free var unifying with a union should bind to the union, "+
				"not produce a mismatch between members")
	})
}

// TestUnifyTypeRefLifetimeArgs covers the parts of the TypeRefType
// unification path that reconcile LifetimeArgs. The empty-TypeArgs
// branch of unifyInner has no source-level path that exercises it
// today (parser does not surface LifetimeArgs and InferLifetimes does
// not populate them), so the unit test constructs the inputs directly.
func TestUnifyTypeRefLifetimeArgs(t *testing.T) {
	// Two TypeRefTypes with the same alias and no TypeArgs but with
	// distinct concrete LifetimeArgs must be reported as a lifetime
	// mismatch. Without explicit reconciliation in the empty-TypeArgs
	// branch, the unifier silently accepts the pair.
	t.Run("empty_typeargs_mismatched_lifetime_args", func(t *testing.T) {
		c := NewChecker(context.Background())
		alias := &type_system.TypeAlias{
			Type: type_system.NewObjectType(nil, nil),
		}
		v1 := &type_system.LifetimeValue{ID: 1, Name: "p"}
		v2 := &type_system.LifetimeValue{ID: 2, Name: "q"}
		ref1 := type_system.NewTypeRefType(nil, "Container", alias)
		ref1.LifetimeArgs = []type_system.Lifetime{v1}
		ref2 := type_system.NewTypeRefType(nil, "Container", alias)
		ref2.LifetimeArgs = []type_system.Lifetime{v2}
		errs := c.Unify(Context{}, ref1, ref2)
		require.NotEmpty(t, errs,
			"distinct LifetimeValues must produce a mismatch error")
	})

	// Mismatched arity on the empty-TypeArgs path is also a structural
	// error — the else branch already enforces this for non-empty
	// TypeArgs; the empty path must too.
	t.Run("empty_typeargs_mismatched_lifetime_arg_arity", func(t *testing.T) {
		c := NewChecker(context.Background())
		alias := &type_system.TypeAlias{
			Type: type_system.NewObjectType(nil, nil),
		}
		v1 := &type_system.LifetimeValue{ID: 1, Name: "p"}
		v2 := &type_system.LifetimeValue{ID: 2, Name: "q"}
		ref1 := type_system.NewTypeRefType(nil, "Container", alias)
		ref1.LifetimeArgs = []type_system.Lifetime{v1, v2}
		ref2 := type_system.NewTypeRefType(nil, "Container", alias)
		ref2.LifetimeArgs = []type_system.Lifetime{v1}
		errs := c.Unify(Context{}, ref1, ref2)
		require.NotEmpty(t, errs,
			"mismatched LifetimeArgs arity must produce an error")
	})
}

// TestSubstituteLifetimes is a structural unit test for the lifetime
// substitution walker. It has no script equivalent because the
// substitution map is a private artifact of generic-function
// instantiation that callers never construct directly — observable
// downstream effects are covered by TestLifetimeUnificationByInference.
func TestSubstituteLifetimes(t *testing.T) {
	// The empty-substitutions fast path can't be observed via a
	// script: every real call site that reaches `SubstituteLifetimes`
	// goes through `instantiateGenericFunc`, which only invokes the
	// walker when `LifetimeParams` is non-empty. The pointer-identity
	// guarantee (return the original type unchanged) is a structural
	// invariant of the walker, not user-visible behavior.
	t.Run("empty_substs_returns_input", func(t *testing.T) {
		obj := type_system.NewObjectType(nil, nil)
		obj.Lifetime = &type_system.LifetimeVar{ID: 1, Name: "a"}
		assert.Same(t, obj, SubstituteLifetimes[type_system.Type](obj, nil))
	})

	// Substitution traversing through a `MutType` wrapper into the
	// inner object's lifetime is exercised end-to-end by every call
	// site involving `mut 'a T` types. From a script, you can confirm
	// the call type-checks, but you can't pin the precise rebuilt-
	// type structure: pruning, printing, and subsequent unification
	// all flatten the result. Asserted directly so the walker's
	// recursion through MutType is locked in.
	t.Run("substitutes_through_mut_into_object_lifetime", func(t *testing.T) {
		v1 := &type_system.LifetimeVar{ID: 1, Name: "a"}
		v2 := &type_system.LifetimeVar{ID: 2, Name: "z"}
		obj := type_system.NewObjectType(nil, nil)
		obj.Lifetime = v1
		mut := type_system.NewMutType(nil, obj)
		out := SubstituteLifetimes[type_system.Type](mut, map[int]type_system.Lifetime{
			1: v2,
		})
		outObj := out.(*type_system.MutType).Type.(*type_system.ObjectType)
		assert.Equal(t, type_system.Lifetime(v2), outObj.Lifetime)
		assert.Equal(t, "mut 'z {}", out.String())
	})

	// Shadowing protection is hard to reproduce from source even
	// though the syntax for `<'a>` on a function exists: every
	// LifetimeVar allocated via `c.FreshLifetimeVar(...)` gets a
	// unique monotonically-increasing ID, so two textually-identical
	// `'a`s on different functions are still distinct numerically and
	// an outer subst map can't accidentally collide with an inner
	// param's ID. The masking exercised here is a defensive
	// structural invariant — it would matter if a future feature
	// (lifetime-bearing type-alias instantiation, manual map
	// construction by callers other than instantiateGenericFunc) ever
	// produced colliding IDs, and unit-testing it directly is the
	// only way to pin the masking behavior since well-behaved
	// instantiation never triggers it.
	t.Run("inner_func_masks_shadowed_lifetime_param", func(t *testing.T) {
		outerVar := &type_system.LifetimeVar{ID: 1, Name: "a"}
		innerVar := &type_system.LifetimeVar{ID: 2, Name: "a"}

		innerObjParam := type_system.NewObjectType(nil, nil)
		innerObjParam.Lifetime = innerVar
		innerObjReturn := type_system.NewObjectType(nil, nil)
		innerObjReturn.Lifetime = innerVar
		innerFn := type_system.NewFuncType(nil, nil, []*type_system.FuncParam{
			{Type: innerObjParam},
		}, innerObjReturn, nil)
		innerFn.LifetimeParams = []*type_system.LifetimeVar{innerVar}

		outerObj := type_system.NewObjectType(nil, nil)
		outerObj.Lifetime = outerVar

		tup := type_system.NewTupleType(nil, outerObj, innerFn)
		repl := &type_system.LifetimeVar{ID: 99, Name: "z"}
		out := SubstituteLifetimes[type_system.Type](tup, map[int]type_system.Lifetime{
			1: repl,
		}).(*type_system.TupleType)

		assert.Equal(t, type_system.Lifetime(repl),
			out.Elems[0].(*type_system.ObjectType).Lifetime,
			"outer occurrence should be replaced")
		outFn := out.Elems[1].(*type_system.FuncType)
		assert.Equal(t, type_system.Lifetime(innerVar),
			outFn.Params[0].Type.(*type_system.ObjectType).Lifetime,
			"inner shadowed param lifetime must be preserved")
		assert.Equal(t, type_system.Lifetime(innerVar),
			outFn.Return.(*type_system.ObjectType).Lifetime,
			"inner shadowed return lifetime must be preserved")
		assert.Equal(t, "['z {}, fn <'a>('a {}) -> 'a {}]", out.String())
	})

	// Substitution must preserve the original type's Provenance so
	// downstream diagnostics still point at the right source span. The
	// canonical Accept-based rebuilds in types.go all thread
	// `t.provenance` through; the lifetime walker must do the same.
	// Source-observable: a generic-instantiation site that produces a
	// type-error after substitution would otherwise lose its span.
	//
	// The kind-specific build functions are the only thing that
	// differs across these cases — the assertion is identical — so a
	// table keeps the structural invariant front-and-center without
	// duplicating per-kind boilerplate.
	provenanceCases := []struct {
		name  string
		build func(prov *ast.NodeProvenance, v1 *type_system.LifetimeVar) type_system.Type
	}{
		{
			name: "object",
			build: func(prov *ast.NodeProvenance, v1 *type_system.LifetimeVar) type_system.Type {
				obj := type_system.NewObjectType(prov, nil)
				obj.Lifetime = v1
				return obj
			},
		},
		{
			name: "typeref",
			build: func(prov *ast.NodeProvenance, v1 *type_system.LifetimeVar) type_system.Type {
				ref := type_system.NewTypeRefType(nil, "Container", nil)
				// NewTypeRefType drops its provenance arg (pre-existing
				// quirk), so set it explicitly via the Type interface.
				ref.SetProvenance(prov)
				ref.LifetimeArgs = []type_system.Lifetime{v1}
				return ref
			},
		},
		{
			name: "tuple",
			build: func(prov *ast.NodeProvenance, v1 *type_system.LifetimeVar) type_system.Type {
				obj := type_system.NewObjectType(nil, nil)
				obj.Lifetime = v1
				return type_system.NewTupleType(prov, obj)
			},
		},
		{
			name: "func",
			build: func(prov *ast.NodeProvenance, v1 *type_system.LifetimeVar) type_system.Type {
				obj := type_system.NewObjectType(nil, nil)
				obj.Lifetime = v1
				return type_system.NewFuncType(prov, nil,
					[]*type_system.FuncParam{{Type: obj}},
					type_system.NewNumPrimType(nil), nil)
			},
		},
		{
			name: "mut",
			build: func(prov *ast.NodeProvenance, v1 *type_system.LifetimeVar) type_system.Type {
				obj := type_system.NewObjectType(nil, nil)
				obj.Lifetime = v1
				return type_system.NewMutType(prov, obj)
			},
		},
	}
	for _, tc := range provenanceCases {
		t.Run("preserves_provenance_through_"+tc.name+"_rebuild", func(t *testing.T) {
			prov := &ast.NodeProvenance{Node: nil}
			v1 := &type_system.LifetimeVar{ID: 1, Name: "a"}
			v2 := &type_system.LifetimeVar{ID: 2, Name: "z"}
			in := tc.build(prov, v1)
			out := SubstituteLifetimes[type_system.Type](in, map[int]type_system.Lifetime{
				1: v2,
			})
			assert.Equal(t, provenance.Provenance(prov), out.Provenance(),
				tc.name+" rebuild must preserve Provenance")
		})
	}

	// `LifetimeArgs` (the lifetime arguments on a constructed type
	// like `Container<'a>`) are not yet expressible from source: the
	// parser does not surface them and `InferLifetimes` does not
	// populate them. This is forward-looking infrastructure for
	// Phase 10/11. Until source-level constructed types carry
	// `LifetimeArgs`, the substitution path can only be exercised by
	// hand-constructed types.
	t.Run("typeref_lifetime_args", func(t *testing.T) {
		v1 := &type_system.LifetimeVar{ID: 1, Name: "a"}
		v2 := &type_system.LifetimeVar{ID: 2, Name: "z"}
		ref := type_system.NewTypeRefType(nil, "Container", nil)
		ref.LifetimeArgs = []type_system.Lifetime{v1}
		out := SubstituteLifetimes[type_system.Type](ref, map[int]type_system.Lifetime{
			1: v2,
		}).(*type_system.TypeRefType)
		require.Len(t, out.LifetimeArgs, 1)
		assert.Equal(t, type_system.Lifetime(v2), out.LifetimeArgs[0])
		assert.Equal(t, "Container<'z>", out.String())
	})
}
