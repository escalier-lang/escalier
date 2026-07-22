package solver

import (
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

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

// A ground `keyof` operand reduces at annotation time (Baseline-D): the evaluator projects
// the operand's keys and unions them. An object yields its property names as string literals,
// a class its instance-member names, a tuple its numeric indices plus "length", and `keyof`
// distributes over a union or intersection. A primitive operand has no enumerable keys, so it
// reduces to `never`.
func TestInferKeyofEagerReduction(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			// The canonical accept case: a ground object reduces to the union of its keys.
			name: "Object",
			src:  `fn g(x: keyof {x: number, y: string}) {}`,
			want: map[string]string{"g": `fn (x: "x" | "y") -> void`},
		},
		{
			// A single-key object collapses to the lone string literal, not a one-member union.
			name: "SingleKeyObject",
			src:  `fn g(x: keyof {only: number}) {}`,
			want: map[string]string{"g": `fn (x: "only") -> void`},
		},
		{
			// keyof distributes over a union operand, so each member's keys union together.
			name: "UnionDistributes",
			src:  `fn g(x: keyof ({a: number} | {b: number})) {}`,
			want: map[string]string{"g": `fn (x: "a" | "b") -> void`},
		},
		{
			// A tuple yields its numeric indices as number literals plus the "length" key.
			name: "Tuple",
			src:  `fn g(x: keyof [number, string]) {}`,
			want: map[string]string{"g": `fn (x: 0 | 1 | "length") -> void`},
		},
		{
			// Distribution reduces the ground members and leaves the non-ground one symbolic:
			// the object contributes "a", the type parameter stays keyof T, and they union.
			name: "UnionWithResidualMember",
			src:  `fn g<T>(x: keyof (T | {a: number})) {}`,
			want: map[string]string{"g": `fn <T>(x: "a" | keyof T) -> void`},
		},
		{
			// A transparent alias to an object expands, so keyof reduces through it.
			name: "AliasToObject",
			src: `
				type Point = {x: number, y: number}
				fn g(x: keyof Point) {}
			`,
			want: map[string]string{"g": `fn (x: "x" | "y") -> void`},
		},
		{
			// keyof over a recursive alias terminates: one expansion yields the object, and
			// projecting its keys never descends into the recursive `children` field value.
			name: "RecursiveAlias",
			src: `
				type Tree = {value: number, children: Tree}
				fn g(x: keyof Tree) {}
			`,
			want: map[string]string{"g": `fn (x: "children" | "value") -> void`},
		},
		{
			// A class projects its instance-member names, the same key set an object yields.
			name: "Class",
			src: `
				class Point {
					x: number,
					y: number,
				}
				fn g(k: keyof Point) {}
			`,
			want: map[string]string{"g": `fn (k: "x" | "y") -> void`},
		},
		{
			// keyof of a primitive is never, since a primitive has no enumerable keys.
			name: "PrimitiveIsNever",
			src:  `fn g(x: keyof number) {}`,
			want: map[string]string{"g": "fn (x: never) -> void"},
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

// An expanding recursive alias under `keyof` terminates instead of looping. Each lap grows the
// type argument, so the instantiation state never repeats and the cycle guard cannot fire; the
// depth budget is the backstop. The alias stays on the active path while `keyof` distributes
// over its union body, so the budget decrements along the recursion and stops it, leaving the
// deepest instantiation as a `keyof A<…>` residual. `keyof A<number>` over `type A<T> = {x: T}
// | A<{y: T}>` reduces the `{x: T}` member of every lap to "x" and leaves the tail symbolic.
func TestInferKeyofExpandingAliasTerminates(t *testing.T) {
	values, _, errs := inferSource(t, `
		type A<T> = {x: T} | A<{y: T}>
		fn g(k: keyof A<number>) {}
	`)
	require.Empty(t, errs)
	require.True(t, strings.HasPrefix(values["g"], `fn (k: "x" | keyof A<{y: `),
		"expected a bounded residual, got %q", values["g"])
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
// value's concrete type directly rather than a residual (M9 PR1a). It resolves a bare name,
// a member chain, and the operand of `keyof typeof x` — the value→type bridge keyof relies
// on. The value's coalesced type keeps its literal (`{a: 1}`), so that is what the query
// yields.
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
		{
			// The canonical `keyof typeof x`: typeof resolves the value to `{a: 1}`, and keyof
			// reduces over that ground object to its single key `"a"` (M9 PR1b).
			name: "KeyofTypeofOperand",
			src: `
				val x = {a: 1}
				fn h(k: keyof typeof x) {}
			`,
			want: map[string]string{"h": `fn (k: "a") -> void`},
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
