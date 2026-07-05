package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M6.5 PR11: enclosing-function lifetimes visible in a nested function ---
//
// A nested function reads an enclosing function's declared lifetimes by name. A closure
// that writes an outer `'a` resolves to that same lifetime variable, so the annotation
// means what it reads and PR10 does not flag the name as undeclared. A name the nested
// function binds in its own `<…>` list still shadows, minting a fresh lifetime. This is a
// naming change, not an inference one, so the computed types do not move.
//
// Each case infers a top-level `outer` and asserts its rendered type. An inherited name
// renders as one shared lifetime across the whole signature; a shadowed name renders as a
// distinct lifetime. No case reports an error, so a stray diagnostic fails the case.
func TestInferNestedLifetime(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		// A closure names the enclosing `'a` in its own signature. `relay` borrows the same
		// region as `outer`'s parameter, so no `<'a>` on `relay` is needed and no undeclared-
		// lifetime error fires. Returning `relay(p)` escapes the inherited borrow out of
		// `outer` at `'a`. Inference ties the two through the `relay(p)` call, so `outer`
		// types as though `relay` were written inline.
		{
			name: "inherited",
			src: `
fn outer<'a>(p: &'a mut {x: number}) -> &'a mut {x: number} {
  val relay = fn (q: &'a mut {x: number}) -> &'a mut {x: number} { return q }
  return relay(p)
}`,
			want: "fn <'a>(p: &'a mut {x: number}) -> &'a mut {x: number}",
		},

		// An inherited lifetime keeps the enclosing function's level, so generalizing the
		// closure at its deeper level leaves the lifetime free rather than quantifying it.
		// Returning the closure surfaces its type: the closure renders `fn (q: &'a mut {…}) ->
		// &'a mut {…}` with no `<'a>` prefix of its own, so `'a` is `outer`'s quantified
		// lifetime, not a fresh one bound by the closure.
		{
			name: "inherited lifetime stays free",
			src: `
fn outer<'a>(p: &'a mut {x: number}) -> fn(q: &'a mut {x: number}) -> &'a mut {x: number} {
  val relay = fn (q: &'a mut {x: number}) -> &'a mut {x: number} { return q }
  return relay
}`,
			want: "fn <'a>(p: &'a mut {x: number}) -> fn (q: &'a mut {x: number}) -> &'a mut {x: number}",
		},

		// A name the closure binds in its own `<…>` list shadows the enclosing lifetime,
		// minting a fresh one. `relay`'s `<'a>` is a new lifetime unrelated to `outer`'s, so
		// the closure is polymorphic in its own `'a` and the `relay(p)` call instantiates it
		// to `outer`'s. The signature still type-checks and reports nothing.
		{
			name: "shadowed by inner clause",
			src: `
fn outer<'a>(p: &'a mut {x: number}) -> &'a mut {x: number} {
  val relay = fn <'a>(q: &'a mut {x: number}) -> &'a mut {x: number} { return q }
  return relay(p)
}`,
			want: "fn <'a>(p: &'a mut {x: number}) -> &'a mut {x: number}",
		},

		// Inheritance chains through more than one enclosing function. `inner` names `'a`,
		// which `outer` binds two levels up; `mid` neither binds nor shadows it, so the name
		// resolves past `mid` to `outer`. Every level borrows the same region and nothing is
		// undeclared.
		{
			name: "chains two levels",
			src: `
fn outer<'a>(p: &'a mut {x: number}) -> &'a mut {x: number} {
  val mid = fn (q: &'a mut {x: number}) -> &'a mut {x: number} {
    val inner = fn (r: &'a mut {x: number}) -> &'a mut {x: number} { return r }
    return inner(q)
  }
  return mid(p)
}`,
			want: "fn <'a>(p: &'a mut {x: number}) -> &'a mut {x: number}",
		},

		// The chain also reaches a nested `fn(…)` type annotation, resolved by
		// resolveFuncTypeAnn rather than inferFunc. The callback parameter's type names `'a`,
		// which resolves to `outer`'s lifetime, so the whole signature shares one `'a` and
		// nothing is undeclared.
		{
			name: "inherited in fn type annotation",
			src: `
fn outer<'a>(p: &'a mut {x: number}, cb: fn(q: &'a mut {x: number}) -> &'a mut {x: number}) -> &'a mut {x: number} {
  return cb(p)
}`,
			want: "fn <'a>(p: &'a mut {x: number}, cb: fn (q: &'a mut {x: number}) -> &'a mut {x: number}) -> &'a mut {x: number}",
		},

		// Inheritance does not depend on the order the enclosing signature first writes the
		// name. Here the callback annotation names `'a` before any parameter or the return
		// does, yet it still resolves to `outer`'s `'a` rather than splitting off a separate
		// lifetime. The whole signature shares one `'a`, the same as when a plain parameter is
		// written first.
		{
			name: "inherited regardless of write order",
			src: `
fn outer<'a>(cb: fn(q: &'a mut {x: number}) -> &'a mut {x: number}, p: &'a mut {x: number}) -> &'a mut {x: number} {
  return cb(p)
}`,
			want: "fn <'a>(cb: fn (q: &'a mut {x: number}) -> &'a mut {x: number}, p: &'a mut {x: number}) -> &'a mut {x: number}",
		},

		// A nested `fn<'a>(…)` type annotation binds `'a` itself, shadowing `outer`'s. The
		// callback's `'a` is a fresh lifetime, so the rendered scheme names it distinctly:
		// `outer` keeps `'a` on its own borrows and the callback carries the separate `'b`.
		// This is the signature-level view of the shadow a closure's `<'a>` makes internally.
		{
			name: "shadowed in fn type annotation",
			src: `
fn outer<'a>(p: &'a mut {x: number}, cb: fn<'a>(q: &'a mut {x: number}) -> &'a mut {x: number}) -> &'a mut {x: number} {
  return cb(p)
}`,
			want: "fn <'a, 'b>(p: &'a mut {x: number}, cb: fn (q: &'b mut {x: number}) -> &'b mut {x: number}) -> &'a mut {x: number}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["outer"])
		})
	}
}
