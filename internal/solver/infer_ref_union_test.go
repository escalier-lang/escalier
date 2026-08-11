package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/dep_graph"
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
			// An `if val` joins its two halves into one value the same way an `if/else`
			// does, so the same ownership rule applies to it.
			name: "mixed ownership across an if val",
			src: `fn f(p: &mut {x: number}, u: number | string) {
  val r = if val n: number = u { p } else { {x: 5} }
  return r
}`,
			wantErrs: []string{"2:10-2:53: " + mixedOwnershipMsg},
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
			// mixed union, so the uniform-ownership check leaves them alone. The join
			// lifetime 'c stays named, with each source lifetime bounded above it.
			name: "uniform borrow union",
			src: `fn f(p: &mut {x: number}, q: &mut {x: number}) {
  if true { return p } else { return q }
}`,
			want: "fn <'a: 'c, 'b: 'c, 'c>(p: &'a mut {x: number}, q: &'b mut {x: number}) -> &'c mut {x: number}",
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

// TestInferDestructureBorrowUnion pins what a pattern binds when it takes apart a scrutinee
// whose type is a union of borrows. Such a union carries no outermost borrow, so the peel
// runs per member: the leaves project out of the peeled members and reach their borrow
// through the binding mode, exactly as they do under a single `&{…}` scrutinee.
//
// Every refutable form is covered, since each seeds the same path binder. Without the peel
// a leaf projects out of `&{x: number}` itself and the owned requirement that projection
// emits reads as a borrow escaping, which is issue #1084.
func TestInferDestructureBorrowUnion(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		want     string   // rendered type of values["f"] when wantErrs is nil
		wantErrs []string // exact diagnostics; nil means the source must check cleanly
	}{
		{
			name: "val else over a borrow union",
			src: `fn f(p: &{x: number} | &{y: string}) {
  val {x: v} = p else { return 0 }
  return v
}`,
			want: "fn (p: &{x: number} | &{y: string}) -> number",
		},
		{
			name: "match over a borrow union",
			src: `fn f(p: &{x: number} | &{y: string}) {
  return match p {
    {x: v} => v,
    _ => 0,
  }
}`,
			want: "fn (p: &{x: number} | &{y: string}) -> number",
		},
		{
			name: "if val over a borrow union",
			src: `fn f(p: &{x: number} | &{y: string}) {
  return if val {x: v} = p { v } else { 0 }
}`,
			want: "fn (p: &{x: number} | &{y: string}) -> number",
		},
		{
			// A function parameter is destructured through bindPattern rather than through the
			// path binder, so it takes the same peel from the other end. The pattern is
			// irrefutable, so nothing narrows the union and `x` reads through both members,
			// picking up the `undefined` the member without it answers.
			name: "destructured parameter of a borrow union",
			src:  `fn f({x: v}: &{x: number} | &{y: string}) { return v }`,
			want: "fn ({x: v}: &{x: number} | &{y: string}) -> number | undefined",
		},
		{
			// Every member is mutable, so the mode is too and the leaf stays writable.
			name: "mut borrow union leaves stay writable",
			src: `fn f(p: &mut {x: {a: number}} | &mut {y: string}) {
  if val {x: v} = p {
    v.a = 2
  }
  return 0
}`,
			want: "fn (p: &mut {x: {a: number}} | &mut {y: string}) -> 0",
		},
		{
			// A leaf reached through an immutable member cannot be written, so the write is
			// rejected rather than silently going through the mutable member.
			name: "immutable borrow union leaves reject a write",
			src: `fn f(p: &{x: {a: number}} | &{y: string}) {
  if val {x: v} = p {
    v.a = 2
  }
  return 0
}`,
			wantErrs: []string{"3:5-3:12: cannot constrain immutable object <: mutable object"},
		},
		{
			// One immutable member makes the whole mode immutable, since a leaf the value may
			// have reached through that member is not writable.
			name: "mixed mutability binds immutable leaves",
			src: `fn f(p: &mut {x: {a: number}} | &{y: string}) {
  if val {x: v} = p {
    v.a = 2
  }
  return 0
}`,
			wantErrs: []string{"3:5-3:12: cannot constrain immutable object <: mutable object"},
		},
		{
			name: "tuple destructure over a borrow union",
			src: `fn f(p: &[number, number] | &[string]) {
  return match p {
    [a, b] => a,
    _ => 0,
  }
}`,
			want: "fn (p: &[number, number] | &[string]) -> number",
		},
		{
			// A leaf of the borrowed initializer names a place inside it, and the fallback is a
			// fresh owned value, so the two cannot join. The declaration reports it rather than
			// leaving the author to meet the mismatch at a later write through the name.
			name: "val else leaf join rejects a mixed-ownership fallback",
			src: `fn f(p: &{x: {a: number}} | &{y: string}) {
  val {x: v} = p else { {x: {a: 1}} }
  return 0
}`,
			wantErrs: []string{"2:3-2:38: " + mixedOwnershipMsg},
		},
		{
			// Each leaf a pattern binds joins separately and every join blames the whole
			// declaration, so the two mixed leaves here report it once.
			name: "val else reports a mixed-ownership declaration once",
			src: `fn f(p: &{x: {a: number}, y: {b: number}} | &{z: string}) {
  val {x: v, y: w} = p else { {x: {a: 1}, y: {b: 2}} }
  return 0
}`,
			wantErrs: []string{"2:3-2:55: " + mixedOwnershipMsg},
		},
		{
			// Both sides of the join are borrows, so ownership is uniform and the leaf binds
			// the union of the two.
			name: "val else leaf join accepts a borrowed fallback",
			src: `fn f(p: &{x: {a: number}} | &{y: string}, q: &{a: number}) {
  val {x: v} = p else { {x: q} }
  return v
}`,
			want: "fn <'a: 'd, 'b: 'd, 'c, 'd>(p: &'a {x: {a: number}} | &'b {y: string}, q: &'c {a: number}) -> &'c {a: number} | &'d {a: number}",
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

// TestBorrowUnionLeafBindsAsBorrow pins the type ONE leaf of a borrow union binds at. Each
// source destructures `p` into `v`, and the assertion is on `v` itself rather than on the
// function's rendered type, which the surrounding returns would otherwise widen.
//
// A borrowable leaf carries the mode's borrow, so it renders under an `&`. The rows that do
// not peel are here to bound the rule: a union is peeled only when every member is a borrow
// carrying a lifetime.
func TestBorrowUnionLeafBindsAsBorrow(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // rendered type of the leaf named `v`
	}{
		{
			name: "immutable members give an immutable leaf",
			src: `fn f(p: &{x: {a: number}} | &{y: string}) {
  val {x: v} = p else { return 0 }
  return 0
}`,
			want: "&{a: number}",
		},
		{
			name: "mutable members give a mutable leaf",
			src: `fn f(p: &mut {x: {a: number}} | &mut {y: string}) {
  val {x: v} = p else { return 0 }
  return 0
}`,
			want: "&mut {a: number}",
		},
		{
			// An owned-mutable `mut {…}` cell carries no lifetime, so it is a value rather than
			// a borrow and its leaves move out.
			name: "owned-mutable members are not peeled",
			src: `fn f(p: mut {x: {a: number}} | mut {y: string}) {
  val {x: v} = p else { return 0 }
  return 0
}`,
			want: "{a: number}",
		},
		{
			// One owned member leaves no single borrow to lift out, so the scrutinee binds owned.
			// Both members carry `x`, so no tag test narrows the union and the leaf reads both.
			name: "a union holding an owned member is not peeled",
			src: `fn f(p: &{x: {a: number}} | {x: {b: string}}) {
  val {x: v} = p else { return 0 }
  return 0
}`,
			want: "{a: number} | {b: string}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			module := parseModule(t, tc.src)
			c := newChecker()
			c.inferDepGraph(sharedPrelude().Child(), 0, module, dep_graph.BuildDepGraph(module))
			require.Empty(t, messagesWithSpan(c.errs))
			leaf := findIdentPat(module, "v")
			require.NotNil(t, leaf)
			require.Equal(t, tc.want, soltype.Print(coalesce(c.info.TypeOf(leaf), soltype.Positive)))
		})
	}
}

// TestBorrowUnionLeafLifetime pins the lifetime a leaf of a borrow union carries. The
// members have no one lifetime between them, so the mode takes their join: a fresh lifetime
// each member's is bounded above, which is what keeps a leaf from outliving the member it
// may have been projected from. joinBorrows unites a set of returned borrows the same way.
//
// Each source returns the leaf from the only branch that returns anything, so the function's
// return type IS the leaf's and the signature shows the outlives bounds.
func TestBorrowUnionLeafLifetime(t *testing.T) {
	t.Run("distinct member lifetimes take a join", func(t *testing.T) {
		values, _, errs := inferSource(t, `fn f(p: &{x: {a: number}} | &{y: string}) {
  if val {x: v} = p {
    return v
  }
}`)
		require.Empty(t, errs)
		// 'c is the join, with the two member lifetimes bounded above it.
		require.Equal(t,
			"fn <'a: 'c, 'b: 'c, 'c>(p: &'a {x: {a: number}} | &'b {y: string}) -> &'c {a: number}",
			values["f"])
	})

	t.Run("one shared member lifetime needs no join", func(t *testing.T) {
		values, _, errs := inferSource(t, `fn f<'a>(p: &'a {x: {a: number}} | &'a {y: string}) {
  if val {x: v} = p {
    return v
  }
}`)
		require.Empty(t, errs)
		require.Equal(t,
			"fn <'a>(p: &'a {x: {a: number}} | &'a {y: string}) -> &'a {a: number}",
			values["f"])
	})
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
