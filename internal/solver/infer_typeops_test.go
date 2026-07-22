package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M9 PR1a: keyof residual node + inert plumbing ---

// A `keyof T` over a type parameter builds the inert KeyofType residual (M9 PR1a): it
// renders `keyof T` and flows through constrain and coalesce without being decomposed or
// reduced, since the evaluator that reduces it lands in PR1b. A ground operand also stays
// symbolic here, and a union operand parenthesizes under the prefix precedence.
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
		{
			// A ground object operand stays symbolic in PR1a — reduction to `"a" | "b"` is
			// PR1b — so the residual renders with the object still inside it.
			name: "GroundOperandStaysSymbolic",
			src:  `fn g(x: keyof {a: number, b: string}) {}`,
			want: map[string]string{"g": "fn (x: keyof {a: number, b: string}) -> void"},
		},
		{
			// A union operand binds looser than the `keyof` prefix, so it parenthesizes.
			name: "UnionOperandParenthesizes",
			src:  `fn g(x: keyof ({a: number} | {b: number})) {}`,
			want: map[string]string{"g": "fn (x: keyof ({a: number} | {b: number})) -> void"},
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
			// The canonical `keyof typeof x`: typeof resolves the value, and keyof wraps the
			// result as an unreduced residual (PR1a), so it renders `keyof {a: 1}`.
			name: "KeyofTypeofOperand",
			src: `
				val x = {a: 1}
				fn h(k: keyof typeof x) {}
			`,
			want: map[string]string{"h": "fn (k: keyof {a: 1}) -> void"},
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
