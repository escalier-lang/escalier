package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// inferTypeNodes infers src and returns the raw soltype.Type of each top-level type binding
// alongside the checker's Context, so a test can reduce a stored residual instead of only reading
// its printed form. An alias binding yields its definition body, the same node inferModule
// prints. It is the raw-type twin of inferModule, test-only.
func inferTypeNodes(t *testing.T, src string) (map[string]soltype.Type, *Context, []SolverError) {
	t.Helper()
	module := parseModule(t, src)
	c := newChecker()
	scope := sharedPrelude().Child()
	c.inferDepGraph(scope, 0, module, dep_graph.BuildDepGraph(module))
	nodes := make(map[string]soltype.Type, len(scope.types))
	for name, b := range scope.types {
		ty := b.Type
		if alias, ok := ty.(*soltype.AliasType); ok {
			if def, ok := c.ctx.aliasDef(alias.Name); ok {
				ty = def.Body
			}
		}
		nodes[name] = ty
	}
	return nodes, c.ctx, c.errs
}

// expandResidual reduces a residual type-level operator such as `keyof Point` against the alias
// environment, the eager expansion constrain performs when it checks a constraint. Production
// keeps a named residual symbolic at annotation and display time, so this test-only helper lets a
// test assert what a residual expands to without routing through a constraint.
func expandResidual(ctx *Context, ty soltype.Type) soltype.Type {
	return newTypeEvaluator(ctx).reduce(ty)
}

// --- M9 PR1a: keyof residual node + inert plumbing ---

// A `keyof T` over a type parameter stays the inert KeyofType residual: T never grounds, so
// the evaluator (M9 PR1b) leaves the operator symbolic, and it renders `keyof T` while
// flowing through constrain and coalesce without being decomposed.
func TestInferKeyofResidual(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			// The residual round-trips through the whole solve: resolveTypeAnn builds it, the
			// body's `return k` flows `keyof T <: keyof T` through constrain reflexively, and
			// coalescing renders it back as `keyof T`.
			name: "TypeParamRoundTrips",
			src:  `fn f<T>(k: keyof T) -> keyof T { return k }`,
			want: map[string]string{"f": "fn <T>(k: keyof T) -> keyof T"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			for name, want := range tt.want {
				require.Equal(t, want, values[name])
			}
		})
	}
}

// --- M9 PR1b: evaluator backbone + keyof reduction ---

// A `keyof` over a structural operand reduces at annotation time: the evaluator projects the
// operand's keys and unions them. Each case defines `type Result = keyof …` and asserts the
// alias's reduced body. An object yields its property names as string literals, a tuple its
// numeric indices, and `keyof` distributes over a union or intersection. A primitive operand has
// no enumerable keys, so it reduces to `never`.
func TestInferKeyofEagerReduction(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string // the reduced body of the `Result` alias the source defines
	}{
		{
			// The canonical accept case: a ground object reduces to the union of its keys.
			name: "Object",
			src:  `type Result = keyof {x: number, y: string}`,
			want: `"x" | "y"`,
		},
		{
			// A single-key object collapses to the lone string literal, not a one-member union.
			name: "SingleKeyObject",
			src:  `type Result = keyof {only: number}`,
			want: `"only"`,
		},
		{
			// keyof distributes over a union operand, so each member's keys union together.
			name: "UnionDistributes",
			src:  `type Result = keyof ({a: number} | {b: number})`,
			want: `"a" | "b"`,
		},
		{
			// A tuple yields only its own numeric indices, the keys Object.keys returns. It omits
			// "length" and the inherited Array.prototype members TypeScript's keyof includes.
			name: "Tuple",
			src:  `type Result = keyof [number, string]`,
			want: "0 | 1",
		},
		{
			// keyof of a primitive is never, since a primitive has no enumerable keys.
			name: "PrimitiveIsNever",
			src:  `type Result = keyof number`,
			want: "never",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, types, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, types["Result"])
		})
	}
}

// `keyof` over a named type reference — an alias or a class — is stored unexpanded, so the type
// keeps the name the source wrote rather than the referenced type's keys. A named reference is
// not expanded at annotation time the way an inline object is; constrain expands it only to check
// a constraint. Each case asserts the stored form renders `keyof Name`, and that reducing it with
// the alias environment — the expansion constrain performs — yields the referenced type's keys.
func TestInferKeyofNamedTypeStaysSymbolic(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantSymbolic string
		wantExpanded string
	}{
		{
			name: "Alias",
			src: `
				type Point = {x: number, y: number}
				type Result = keyof Point
			`,
			wantSymbolic: "keyof Point",
			wantExpanded: `"x" | "y"`,
		},
		{
			name: "RecursiveAlias",
			src: `
				type Tree = {value: number, children: Tree}
				type Result = keyof Tree
			`,
			wantSymbolic: "keyof Tree",
			wantExpanded: `"children" | "value"`,
		},
		{
			name: "GenericAlias",
			src: `
				type Box<T> = {value: T}
				type Result = keyof Box<number>
			`,
			wantSymbolic: "keyof Box<number>",
			wantExpanded: `"value"`,
		},
		{
			name: "Class",
			src: `
				class Point {
					x: number,
					y: number,
				}
				type Result = keyof Point
			`,
			wantSymbolic: "keyof Point",
			wantExpanded: `"x" | "y"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			result := nodes["Result"]
			// The stored form stays symbolic: a named operand is not expanded at annotation time.
			require.Equal(t, tt.wantSymbolic, soltype.Print(result))
			// Reducing with the alias environment — what constrain does to check a constraint —
			// expands the named operand to the referenced type's keys.
			require.Equal(t, tt.wantExpanded, soltype.Print(expandResidual(ctx, result)))
		})
	}
}

// A signature keeps `keyof Point` rather than the expanded keys, the display the residual is
// stored for. This is the canonical case: `fn g(k: keyof Point)` infers `fn (k: keyof Point)`,
// not `fn (k: "x" | "y")`.
func TestInferKeyofClassSignature(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Point {
			x: number,
			y: number,
		}
		fn g(k: keyof Point) {}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (k: keyof Point) -> void`, values["g"])
}

// constrain expands a `keyof` residual over a type alias or class to check satisfaction, while
// the stored type stays named. A key the referenced type's key set contains is accepted; one it
// lacks is rejected against the expanded keys, so the diagnostic names the projected union. The
// expansion runs at every constraint site: a `val` annotation, a generic alias instantiation, an
// alias that forwards to another alias, and an argument checked against a parameter's type.
func TestInferKeyofAliasConstraint(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "AliasMemberAccepted",
			src: `
				type Point = {x: number, y: number}
				val k: keyof Point = "x"
			`,
		},
		{
			name: "AliasNonMemberRejected",
			src: `
				type Point = {x: number, y: number}
				val k: keyof Point = "z"
			`,
			wantErr: `cannot constrain "z" <: "x" | "y"`,
		},
		{
			name: "GenericAliasMemberAccepted",
			src: `
				type Box<T> = {value: T}
				val k: keyof Box<number> = "value"
			`,
		},
		{
			name: "GenericAliasNonMemberRejected",
			src: `
				type Box<T> = {value: T}
				val k: keyof Box<number> = "size"
			`,
			wantErr: `cannot constrain "size" <: "value"`,
		},
		{
			name: "AliasForwardingToAlias",
			src: `
				type Point = {x: number, y: number}
				type P2 = Point
				val k: keyof P2 = "y"
			`,
		},
		{
			name: "ClassMemberAccepted",
			src: `
				class Point {
					x: number,
					y: number,
				}
				val k: keyof Point = "x"
			`,
		},
		{
			name: "CallArgumentAccepted",
			src: `
				type Point = {x: number, y: number}
				fn pick(k: keyof Point) -> number { return 1 }
				val r = pick("x")
			`,
		},
		{
			name: "CallArgumentRejected",
			src: `
				type Point = {x: number, y: number}
				fn pick(k: keyof Point) -> number { return 1 }
				val r = pick("z")
			`,
			wantErr: `cannot constrain "z" <: "x" | "y"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			if tt.wantErr == "" {
				require.Empty(t, errs)
				return
			}
			require.Len(t, errs, 1)
			require.Equal(t, tt.wantErr, errs[0].Message())
		})
	}
}

// A `keyof Point` on both sides of a flow checks reflexively without expanding: `keyof Point <:
// keyof Point` succeeds by structural equality on the residual, so the identity function keeps
// the name on both its parameter and its return.
func TestInferKeyofAliasIdentity(t *testing.T) {
	values, _, errs := inferSource(t, `
		type Point = {x: number, y: number}
		fn id(k: keyof Point) -> keyof Point { return k }
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (k: keyof Point) -> keyof Point`, values["id"])
}

// Distribution reduces the ground members and leaves the non-ground one symbolic: the object
// contributes "a", the type parameter stays keyof T, and they union. This uses a function so the
// scheme renders the parameter as `T`; an alias body would render it as a raw inference var.
func TestInferKeyofUnionWithResidualMember(t *testing.T) {
	values, _, errs := inferSource(t, `fn g<T>(x: keyof (T | {a: number})) {}`)
	require.Empty(t, errs)
	require.Equal(t, `fn <T>(x: "a" | keyof T) -> void`, values["g"])
}

// A nested `keyof keyof` terminates instead of looping on the same symbolic shape. Over a type
// parameter neither operator grounds, so it stays the `keyof keyof T` residual. Over a ground
// object the inner keyof reduces to a union of string literals, and keyof of those literals is
// never, since a string literal has no enumerable keys.
func TestInferKeyofNested(t *testing.T) {
	t.Run("TypeParamStaysSymbolic", func(t *testing.T) {
		values, _, errs := inferSource(t, `fn f<T>(k: keyof keyof T) {}`)
		require.Empty(t, errs)
		require.Equal(t, "fn <T>(k: keyof keyof T) -> void", values["f"])
	})
	t.Run("GroundObjectReducesToNever", func(t *testing.T) {
		_, types, errs := inferSource(t, `type Result = keyof keyof {a: number, b: string}`)
		require.Empty(t, errs)
		require.Equal(t, "never", types["Result"])
	})
}

// A rejected constraint whose subject is a `keyof` residual names it structurally in the
// diagnostic — `cannot constrain keyof t1 <: number` rather than the bare `?` the default
// describe arm would render — so the inert node stays legible in error messages. describe is
// the raw mid-constrain renderer, so the operand shows as the raw var `t1` rather than the
// param name `T` the coalesced printer would use.
func TestInferKeyofResidualErrorMessage(t *testing.T) {
	_, _, errs := inferSource(t, `fn f<T>(k: keyof T) -> number { return k }`)
	require.Len(t, errs, 1)
	require.IsType(t, &CannotConstrainError{}, errs[0])
	require.Equal(t, "1:12-1:19: cannot constrain keyof t1 <: number", msgWithSpan(errs[0]))
}

// Checking a value against `keyof` of an expanding recursive alias terminates instead of looping.
// The reduction is budget-truncated and leaves a `keyof A<…>` residual, so constrain does not
// recurse on it — re-expanding would grow the operand without bound — and the residual stays
// inert, conservatively rejecting the value. The point of the test is termination; the precise
// rejection is a consequence of the truncation, which CheckRegular will reject at definition time
// in a later milestone.
func TestInferKeyofExpandingAliasTerminates(t *testing.T) {
	_, _, errs := inferSource(t, `
		type A<T> = {x: T} | A<{y: T}>
		val k: keyof A<number> = "x"
	`)
	require.Len(t, errs, 1)
	require.IsType(t, &CannotConstrainError{}, errs[0])
}

// A `keyof` residual whose operand is an inference variable stays symbolic through the value
// solve, then reduces at coalescing once the variable gains a concrete object bound (Design-A,
// the post-solve reduction site). The positive-position variable inlines to its lower bound
// `{a: number, b: string}`, and the coalescer's ExitType sweep projects that object's keys to
// `"a" | "b"`. Source cannot yet reach this path — the operand-grounding case needs `keyof
// typeof <param>`, and typeof of a parameter is not a readable value in PR1a — so the reduction
// is exercised at the coalesce boundary directly.
func TestInferKeyofPostSolveReduction(t *testing.T) {
	c := &Context{}
	v := c.freshVar(0)
	v.LowerBounds = []soltype.Type{exactObj(propElem("a", num()), propElem("b", str()))}
	got := coalesce(&soltype.KeyofType{Operand: v}, soltype.Positive)
	require.Equal(t, `"a" | "b"`, soltype.Print(got))
}

// A `typeof v` query resolves against the value scope at annotation time, returning the
// value's concrete type directly rather than a residual (M9 PR1a). It resolves a bare name and
// a member chain. The value's coalesced type keeps its literal `{a: 1}`, so that is what the
// query yields.
func TestInferTypeof(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			// A bare `typeof v` resolves to the value's object type, which the annotated
			// binding then adopts.
			name: "BareValue",
			src: `
				val v = {a: 1}
				val w: typeof v = v
			`,
			want: map[string]string{"w": "{a: 1}"},
		},
		{
			// A member chain `typeof p.inner` resolves the base value and projects the named
			// property off it.
			name: "MemberChain",
			src: `
				val p = {inner: {a: 1}}
				val w: typeof p.inner = {a: 1}
			`,
			want: map[string]string{"w": "{a: 1}"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			for name, want := range tt.want {
				require.Equal(t, want, values[name])
			}
		})
	}
}

// The canonical `keyof typeof x`, the value→type bridge: typeof resolves the value `x` to its
// object type `{a: 1}` at annotation time, and keyof reduces over that ground object to its
// single key `"a"`.
func TestInferKeyofTypeofValue(t *testing.T) {
	_, types, errs := inferSource(t, `
		val x = {a: 1}
		type Result = keyof typeof x
	`)
	require.Empty(t, errs)
	require.Equal(t, `"a"`, types["Result"])
}
