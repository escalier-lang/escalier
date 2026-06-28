package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// mixedOwnershipMsg is the MixedOwnershipError message, asserted with a span prefix
// in the mixed-ownership rows below.
const mixedOwnershipMsg = "a union or intersection mixes owned and borrowed members. Make ownership uniform first. Clone the borrowed member to own it, or borrow the owned member."

// TestInferRefUnion pins binding `f`'s rendered type when wantErrs is nil, else asserts the exact diagnostics.
func TestInferRefUnion(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		want     string   // rendered type of values["f"] when wantErrs is nil
		wantErrs []string // exact diagnostics; nil means the source must check cleanly
	}{
		// --- unions/intersections as RefInner ---
		{
			name: "immutable borrow over a union",
			src:  `fn f(p: &({a: number} | {b: number})) { return p }`,
			want: "fn <'a>(p: &'a ({a: number} | {b: number})) -> &'a ({a: number} | {b: number})",
		},
		{
			name: "mutable borrow over a union",
			src:  `fn f(p: &mut ({a: number} | {b: number})) { return p }`,
			want: "fn <'a>(p: &'a mut ({a: number} | {b: number})) -> &'a mut ({a: number} | {b: number})",
		},
		{
			// The union joins RefInner, so the `mut` wrapper has a borrowable pointee to
			// wrap rather than reporting an unsupported feature.
			name: "owned-mutable union accepted",
			src:  `fn f(p: mut ({a: number} | {b: number})) { return p }`,
			want: "fn (p: mut ({a: number} | {b: number})) -> mut ({a: number} | {b: number})",
		},
		{
			name: "immutable borrow over an intersection",
			src:  `fn f(p: &({a: number} & {b: number})) { return p }`,
			want: "fn <'a>(p: &'a ({a: number} & {b: number})) -> &'a ({a: number} & {b: number})",
		},

		// --- mixed-ownership rejection at join sites ---
		{
			name: "mixed ownership in an if/else value",
			src: `fn f(p: &mut {x: number}) {
  val q = if true { p } else { {x: 5} }
  return q
}`,
			wantErrs: []string{"2:10-2:40: " + mixedOwnershipMsg},
		},
		{
			name: "mixed ownership across return points",
			src: `fn f(p: &mut {x: number}) {
  if true { return p } else { return {x: 5} }
}`,
			wantErrs: []string{"1:1-3:2: " + mixedOwnershipMsg},
		},
		{
			name: "mixed ownership across match arms",
			src: `fn f(p: &mut {x: number}) {
  val r = match 1 {
    1 => p,
    _ => ({x: 5}),
  }
  return r
}`,
			wantErrs: []string{"2:11-5:4: " + mixedOwnershipMsg},
		},
		{
			name: "uniform owned union",
			src: `fn f() {
  if true { return {x: 5} } else { return {x: 6} }
}`,
			want: "fn () -> {x: 5} | {x: 6}",
		},
		{
			// Value types carry no ownership, so a union of them never trips the check.
			name: "uniform value union",
			src: `fn f() {
  if true { return 5 } else { return "x" }
}`,
			want: `fn () -> 5 | "x"`,
		},
		{
			// Two borrows that differ only in lifetime join into one borrow rather than a
			// mixed union, so the uniform-ownership check leaves them alone.
			name: "uniform borrow union",
			src: `fn f(p: &mut {x: number}, q: &mut {x: number}) {
  if true { return p } else { return q }
}`,
			want: "fn <'a, 'b>(p: &'a mut {x: number}, q: &'b mut {x: number}) -> &('a | 'b) mut {x: number}",
		},

		// --- nested-borrow normalization ---
		//
		// A borrow whose pointee is itself a borrow collapses to depth one, since the JS
		// target compiles every borrow to the same bare object reference.
		{
			name: "immutable nested borrow collapses to depth one",
			src:  `fn f(p: &(&{x: number})) { return p }`,
			want: "fn <'a>(p: &'a {x: number}) -> &'a {x: number}",
		},
		{
			// `&mut (&mut {x})` would have to repoint the inner borrow, which needs a
			// storage cell the JS target cannot express, so it is uninhabitable.
			name:     "mutable nested borrow rejected",
			src:      `fn f(p: &mut (&mut {x: number})) { return p }`,
			wantErrs: []string{"1:9-1:31: Unsupported: mutable borrow of a borrow is uninhabitable"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			if tc.wantErrs != nil {
				require.Equal(t, tc.wantErrs, messagesWithSpan(errs))
				return
			}
			require.Empty(t, errs)
			require.Equal(t, tc.want, values["f"])
		})
	}
}

// TestConstrainRefUnion pins the variance of a borrow over a union pointee at the
// constraint level. A mutable borrow is invariant in its pointee, an immutable borrow
// factors covariantly, and a bare owned union auto-borrows into a borrow destination.
func TestConstrainRefUnion(t *testing.T) {
	unionXY := func() *soltype.UnionType {
		return &soltype.UnionType{Types: []soltype.Type{
			exactObj(propElem("x", num())),
			exactObj(propElem("y", str())),
		}}
	}
	cases := []struct {
		name    string
		build   func(c *Context) (sub, super soltype.Type)
		wantErr bool
	}{
		{
			// mut A </: mut (A | B): a mutable borrow is invariant in its pointee.
			name: "mutable borrow pointee is invariant",
			build: func(c *Context) (soltype.Type, soltype.Type) {
				sub := &soltype.RefType{Mut: true, Inner: exactObj(propElem("x", num()))}
				super := &soltype.RefType{Mut: true, Inner: unionXY()}
				return sub, super
			},
			wantErr: true,
		},
		{
			// &A <: &(A | B): an immutable borrow is covariant in its pointee.
			name: "immutable borrow pointee factors",
			build: func(c *Context) (soltype.Type, soltype.Type) {
				sub := &soltype.RefType{Lt: c.freshLifetime(0), Inner: exactObj(propElem("x", num()))}
				super := &soltype.RefType{Lt: c.freshLifetime(0), Inner: unionXY()}
				return sub, super
			},
			wantErr: false,
		},
		{
			// (A | B) <: &(A | B): a bare owned union auto-borrows into a borrow.
			name: "bare owned union auto-borrows into a borrow",
			build: func(c *Context) (soltype.Type, soltype.Type) {
				return unionXY(), &soltype.RefType{Lt: c.freshLifetime(0), Inner: unionXY()}
			},
			wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Context{}
			sub, super := tc.build(c)
			errs := c.Constrain(sub, super)
			if tc.wantErr {
				require.NotEmpty(t, errs)
			} else {
				require.Empty(t, errs)
			}
		})
	}
}
