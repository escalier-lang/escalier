package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M6.5 PR8: lower declared lifetime bounds at no-body sites ---
//
// A `declare fn` has no body to infer an outlives relation from, so its signature is
// the whole contract. Each declared `<'a: 'b>` bound lowers into constrainLt during
// signature resolution, the same lowering a function type annotation uses, so the
// bound solves like one a body would infer and the printer renders it back. This is the
// no-body twin of the body-carrying check in infer_declared_lifetime_bound_test.go: a
// body-carrying function proves its bounds, a declare fn declares them.

// A declare fn's signature renders as written and reports no error. It carries a nil
// body, so inferFunc types it as a no-body site: it adopts the declared return and
// lowers its declared bounds rather than checking them against a body that has none.
func TestInferDeclareFnLifetimeBounds(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		binding string
		want    string
	}{
		// A declare fn with no lifetimes adopts its return annotation. A no-body site must
		// not constrain `void <: number`, which a body-carrying path would.
		{
			name:    "no lifetimes adopts return",
			src:     `declare fn now() -> number`,
			binding: "now",
			want:    "fn () -> number",
		},
		// A named borrow reaching the output shares one lifetime across the parameter and
		// return, quantified as `'a` on both.
		{
			name:    "named borrow shares one lifetime",
			src:     `declare fn id<'a>(p: &'a {x: number}) -> &'a {x: number}`,
			binding: "id",
			want:    "fn <'a>(p: &'a {x: number}) -> &'a {x: number}",
		},
		// A declared `'b: 'a` bound relates the two lifetimes. Only p is returned, so `'b`
		// reaches no output on its own, yet the bound keeps it named and renders back as
		// `'b: 'a`, since 'b outlives 'a.
		{
			name:    "outlives bound keeps connected lifetime",
			src:     `declare fn f<'a, 'b: 'a>(p: &'a {x: number}, q: &'b {x: number}) -> &'a {x: number}`,
			binding: "f",
			want:    "fn <'a, 'b: 'a>(p: &'a {x: number}, q: &'b {x: number}) -> &'a {x: number}",
		},
		// Without a bound the same unconnected `'b` elides to a bare `&`, isolating the
		// bound's effect above.
		{
			name:    "no bound elides unconnected lifetime",
			src:     `declare fn f<'a, 'b>(p: &'a {x: number}, q: &'b {x: number}) -> &'a {x: number}`,
			binding: "f",
			want:    "fn <'a>(p: &'a {x: number}, q: &{x: number}) -> &'a {x: number}",
		},
		// A multi-source join shape declared at a no-body site. Each source outlives the
		// output `'c`, so the two `'c` bounds render in the prefix.
		{
			name:    "multi-source join bounds",
			src:     `declare fn pick<'a: 'c, 'b: 'c, 'c>(p: &'a mut {x: number}, q: &'b mut {x: number}) -> &'c mut {x: number}`,
			binding: "pick",
			want:    "fn <'a: 'c, 'b: 'c, 'c>(p: &'a mut {x: number}, q: &'b mut {x: number}) -> &'c mut {x: number}",
		},
		// A declared `'a: 'static` lowers to constrainLt('a, 'static). 'static is the bottom
		// of the outlives lattice and absorbs the meet, so 'a resolves to 'static and the
		// borrows render `&'static`.
		{
			name:    "static bound forces static",
			src:     `declare fn g<'a: 'static>(p: &'a {x: number}) -> &'a {x: number}`,
			binding: "g",
			want:    "fn (p: &'static {x: number}) -> &'static {x: number}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values[tt.binding])
		})
	}
}

// A `declare fn` inside a fully-annotated overload set is a no-body site too. Its nil
// body keeps a declare arm from constraining a synthetic `void` against its declared
// return, the same spurious `void <: T` an overloaded regular function already avoids.
//
// The rendered arm strips its borrow lifetimes to bare `&`, e.g. `p: &{x: number}` rather
// than `p: &'a {x: number}`. That elision is a general property of the signature-bound
// overload path: a body-carrying `fn h<'a>(p: &'a {…}) -> &'a {…}` overload renders the
// same way, since a fully-annotated arm's signature is bound from a probe that does not
// carry its per-arm lifetime generalization into the scheme. Surfacing overload-arm
// lifetimes is separate from this milestone; the point here is that the no-body arm no
// longer errors.
func TestInferOverloadedDeclareFnNoSpuriousError(t *testing.T) {
	values, _, errs := inferSource(t, `
		declare fn g<'a, 'b: 'a>(p: &'a {x: number}, q: &'b {x: number}) -> &'a {x: number}
		declare fn g(x: string) -> string`)
	require.Empty(t, errs)
	require.Equal(t,
		"(fn (p: &{x: number}, q: &{x: number}) -> &{x: number}) & (fn (x: string) -> string)",
		values["g"])
}
