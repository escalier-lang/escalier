package solver

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- PR 13: deep, uniform `mut` and `readonly` ---

// `mut` is deep: every nested layer becomes writable, so `p.a.x = 5` is legal
// through `mut {a: {x}}` and `&mut {a: {x}}`.
func TestDeepMutEnablesNestedWrite(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "owned mut, one level",
			src:  "fn f(p: mut {a: {x: number}}) { p.a.x = 5 }",
			want: "fn (p: mut {a: {x: number}}) -> undefined",
		},
		{
			name: "borrowed &mut, one level",
			src:  "fn f(p: &mut {a: {x: number}}) { p.a.x = 5 }",
			want: "fn (p: &mut {a: {x: number}}) -> undefined",
		},
		{
			name: "owned mut, three levels",
			src:  "fn f(p: mut {a: {b: {c: number}}}) { p.a.b.c = 5 }",
			want: "fn (p: mut {a: {b: {c: number}}}) -> undefined",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Empty(t, errs)
			require.Equal(t, tc.want, values["f"])
		})
	}
}

// An immutable annotation stays immutable end to end, so a nested write through
// either an owned-immutable or `&` borrow receiver is rejected.
func TestImmutableAnnotationRejectsNestedWrite(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"owned immutable", "fn f(p: {a: {x: number}}) { p.a.x = 5 }", "1:29-1:38: cannot constrain immutable object <: mutable object"},
		{"immutable borrow", "fn f(p: &{a: {x: number}}) { p.a.x = 5 }", "1:30-1:39: cannot constrain immutable object <: mutable object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			// The inner field-read result is a fresh variable; the message resolves it
			// to its concrete bound so it reads `object`, not an internal `t{N}`.
			require.Equal(t, []string{tc.want}, messagesWithSpan(t, errs))
		})
	}
}

// A fully fresh literal binds to a deeply-mutable annotation at every level.
func TestDeepMutLowersFreshLiteral(t *testing.T) {
	values, _, errs := inferSource(t, `val w: mut {a: {b: {c: number}}} = {a: {b: {c: 0}}}`)
	require.Empty(t, errs)
	require.Equal(t, "mut {a: {b: {c: number}}}", values["w"])
}

// An explicit nested `mut` field or element inside a non-mut container is rejected at
// the annotation site (#779): `mut {a: mut {x}}` and `mut [mut {x}]` each carry an
// owned-mut cell on a field of an inner immutable container. The cell is recovered to
// its bare inner so the surrounding annotation still resolves, and the fresh literal
// upgrades into that bare target.
func TestNestedMutFieldAnnotationRejected(t *testing.T) {
	const msg = "owned-mutable field annotation is not allowed; the enclosing context decides mutability — wrap the whole annotation in `mut` to make this field writable, or use interior mutability"
	t.Run("object", func(t *testing.T) {
		values, _, errs := inferSource(t, `val w: mut {a: mut {x: number}} = {a: {x: 0}}`)
		require.Equal(t, []string{"1:20-1:21: " + msg}, messagesWithSpan(t, errs))
		require.Equal(t, "mut {a: {x: number}}", values["w"])
	})
	t.Run("tuple", func(t *testing.T) {
		values, _, errs := inferSource(t, `val w: mut [mut {x: number}] = [{x: 0}]`)
		require.Equal(t, []string{"1:17-1:18: " + msg}, messagesWithSpan(t, errs))
		require.Equal(t, "mut [{x: number}]", values["w"])
	})
}

// `readonly` rejects `obj.a = …` even on an owned-mutable enclosing object.
func TestReadonlyRejectsFieldReassignment(t *testing.T) {
	_, _, errs := inferSource(t, "fn f(obj: mut {readonly a: number}) { obj.a = 5 }")
	require.Equal(t, []string{"1:39-1:48: cannot assign to readonly property: a"}, messagesWithSpan(t, errs))
}

// `readonly` forbids reassigning the field but not mutating through it: `obj.a.b
// = 5` checks while `obj.a = …` is rejected.
func TestReadonlyPermitsValueMutationButNotReassignment(t *testing.T) {
	t.Run("mutate the value", func(t *testing.T) {
		values, _, errs := inferSource(t, "fn f(obj: mut {readonly a: {b: number}}) { obj.a.b = 5 }")
		require.Empty(t, errs)
		require.Equal(t, "fn (obj: mut {readonly a: {b: number}}) -> undefined", values["f"])
	})
	t.Run("reassign the field", func(t *testing.T) {
		_, _, errs := inferSource(t, "fn f(obj: mut {readonly a: {b: number}}) { obj.a = {b: 9} }")
		require.Equal(t, []string{"1:44-1:58: cannot assign to readonly property: a"}, messagesWithSpan(t, errs))
	})
}

// An explicit `&obj.f` on a borrow field flows the field's own borrow through
// rather than re-anchoring to the receiver, matching the implicit-read path.
func TestExplicitBorrowOfBorrowFieldFlowsFieldLifetime(t *testing.T) {
	t.Run("immutable borrow field", func(t *testing.T) {
		src := "fn f(obj: {a: &{x: number}}) { return &obj.a }"
		values, _, errs := inferSource(t, src)
		require.Empty(t, errs)
		require.Equal(t, "fn <'a>(obj: {a: &'a {x: number}}) -> &'a {x: number}", values["f"])
	})
	t.Run("mutable borrow field", func(t *testing.T) {
		src := "fn f(obj: {a: &mut {x: number}}) { return &obj.a }"
		values, _, errs := inferSource(t, src)
		require.Empty(t, errs)
		require.Equal(t, "fn <'a>(obj: {a: &'a mut {x: number}}) -> &'a mut {x: number}", values["f"])
	})
}

// Borrowing a field of a deep-mut container with `&mut obj.a` yields a clean
// `&mut {x: number}`: the deep-mut read view of `a` off the mutable receiver is a
// mutable borrow of its bare inner. Routing the read through the fresh check var
// instead would let the co-occurrence pass widen it to a union and strip the borrow.
func TestExplicitBorrowOfDeepMutFieldPeels(t *testing.T) {
	src := "fn f(p: mut {a: {x: number}}) { return &mut p.a }"
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {a: {x: number}}) -> &mut {x: number}", values["f"])
}

// A readonly source field can't fill a writable target field, but the reverse is
// fine. A readonly target supports only the covariant read view, so a wider
// source can fill it through width subtyping.
func TestReadonlySubtypingFlowsThroughCallAndReturn(t *testing.T) {
	t.Run("call: readonly source into writable param", func(t *testing.T) {
		src := `fn sink(o: mut {a: number}) {}
fn f(obj: mut {readonly a: number}) { sink(obj) }`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"2:39-2:48: readonly field a cannot satisfy a writable field requirement"}, messagesWithSpan(t, errs))
	})
	t.Run("return: readonly source as writable return", func(t *testing.T) {
		src := "fn f(obj: mut {readonly a: number}) -> mut {a: number} { return obj }"
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"1:1-1:70: readonly field a cannot satisfy a writable field requirement"}, messagesWithSpan(t, errs))
	})
	t.Run("call: writable source into readonly param is fine", func(t *testing.T) {
		src := `fn sink(o: mut {readonly a: number}) {}
fn f(obj: mut {a: number}) { sink(obj) }`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("call: wider source field fills inexact readonly target", func(t *testing.T) {
		// The write-back is skipped for readonly targets, so width subtyping accepts.
		src := `fn sink(o: mut {readonly a: {x: number, ...}}) {}
fn f(obj: mut {a: {x: number, y: number}}) { sink(obj) }`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
}

// An owned-mutable field inside an immutable container — `{a: mut {x}}` — is now
// rejected at the annotation site (#779): interior mutability is the proper mechanism
// for that case (#618), and inference never produces the shape either, so the type is
// no longer reachable. The annotation reports MutFieldError and recovers to the bare
// `{a: {x}}`; the body's write through the immutable receiver then fails as before.
func TestOwnedMutFieldAnnotationRejected(t *testing.T) {
	src := "fn f(p: {a: mut {x: number}}) { p.a.x = 5 }"
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"1:17-1:18: owned-mutable field annotation is not allowed; the enclosing context decides mutability — wrap the whole annotation in `mut` to make this field writable, or use interior mutability",
		"1:33-1:42: cannot constrain immutable object <: mutable object",
	}, messagesWithSpan(t, errs))
}

// Chained reads through three deep-mut layers stay mutable, so a depth-3 write
// checks.
func TestDeepMutChainedReadsAllowDeepWrite(t *testing.T) {
	src := "fn f(p: mut {a: {b: {c: number}}}) { p.a.b.c = 5 }"
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {a: {b: {c: number}}}) -> undefined", values["f"])
}

// A readonly field on an immutable container still reports the readonly error,
// not the immutable-receiver one.
func TestReadonlyFieldOnImmutableContainerStillRejectsWrite(t *testing.T) {
	src := "fn f(p: {readonly a: number}) { p.a = 5 }"
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{"1:33-1:40: cannot assign to readonly property: a"}, messagesWithSpan(t, errs))
}

// The fresh-literal upgrade reaches into tuples too.
func TestDeepMutLowersFreshTupleLiteral(t *testing.T) {
	values, _, errs := inferSource(t, "val w: mut [number, {x: number}] = [1, {x: 0}]")
	require.Empty(t, errs)
	require.Equal(t, "mut [number, {x: number}]", values["w"])
}

// A readonly field's value is still deep-mutable, so multiple writes through it
// check independently.
func TestReadonlyFieldValueIsDeepMutable(t *testing.T) {
	src := `fn f(obj: mut {readonly a: {x: number, y: number}}) { obj.a.x = 5
 obj.a.y = 6 }`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {readonly a: {x: number, y: number}}) -> undefined", values["f"])
}

// `readonly a: number` round-trips through the printer.
func TestReadonlyRendersOnReadField(t *testing.T) {
	values, _, errs := inferSource(t, "fn f(obj: {readonly a: number}) -> number { return obj.a }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: {readonly a: number}) -> number", values["f"])
}

// --- PR 14: lazy deep-mut representation ---

// Under the lazy form (PR 14), the mut-context flag pins a nested field invariant
// inside a mutable wrapper. A field whose type is a strict subtype of the target's
// field passes the covariant read view but fails the contravariant write view, so a
// `mut {a: {x: number}}` value cannot fill a `mut {a: {x: number | string}}` destination —
// the same invariance the eager per-cell `mut` lowering produced.
func TestLazyDeepMutPinsNestedFieldInvariant(t *testing.T) {
	src := `fn sink(q: mut {a: {x: number | string}}) {}
fn f(p: mut {a: {x: number}}) { sink(p) }`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{"2:33-2:40: cannot constrain string <: number"}, messagesWithSpan(t, errs))
}

// The same shapes under an immutable wrapper are covariant, so the strict-subtype
// field is accepted through ordinary width/depth subtyping. This is the contrast
// that the mut-context flag draws: invariant inside a mutable wrapper, covariant
// outside one.
func TestImmutableWrapperKeepsNestedFieldCovariant(t *testing.T) {
	src := `fn sink(q: {a: {x: number | string}}) {}
fn f(p: {a: {x: number}}) { sink(p) }`
	_, _, errs := inferSource(t, src)
	require.Empty(t, errs)
}

// --- #779: no owned-mutable field inside a non-mut container ---

// A `&`/`&mut` borrow field inside a non-mut container stays legal: it references
// external storage, not an interior owned-mutable cell, so #779 leaves it alone.
func TestBorrowFieldInImmutableContainerIsLegal(t *testing.T) {
	t.Run("shared borrow field", func(t *testing.T) {
		values, _, errs := inferSource(t, "fn f(p: {a: &{x: number}}) -> &{x: number} { return p.a }")
		require.Empty(t, errs)
		require.Equal(t, "fn <'a>(p: {a: &'a {x: number}}) -> &'a {x: number}", values["f"])
	})
	t.Run("mutable borrow field", func(t *testing.T) {
		values, _, errs := inferSource(t, "fn f(p: {a: &mut {x: number}}) { p.a.x = 5 }")
		require.Empty(t, errs)
		require.Equal(t, "fn (p: {a: &mut {x: number}}) -> undefined", values["f"])
	})
}

// The #779 round-trip: a nested write infers a mut container whose displayed
// signature is a valid annotation, and a caller passing exactly that displayed type
// is accepted. Inference never produces a `mut` field inside a non-mut container, so
// what it prints can always be written back.
func TestNestedWriteInfersMutContainerAndRoundTrips(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "one level",
			src:  "fn foo(obj) { obj.p.x = 5 }",
			want: "fn (obj: mut {p: {x: number}}) -> undefined",
		},
		{
			name: "three levels",
			src:  "fn foo(obj) { obj.a.b.c = 5 }",
			want: "fn (obj: mut {a: {b: {c: number}}}) -> undefined",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Empty(t, errs)
			require.Equal(t, tc.want, values["foo"])
		})
	}

	// Re-feeding the displayed type as a caller's annotation type-checks, so the
	// signature genuinely round-trips.
	t.Run("round-trip caller", func(t *testing.T) {
		src := `fn foo(obj) { obj.p.x = 5 }
fn caller(a: mut {p: {x: number}}) { foo(a) }`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
}

// --- #617: receiver mutability reaches a field through every receiver shape ---

// mutReceiverCarriers is the five ways a source can name the receiver a nested write goes
// through. Each entry builds a program writing `c.inner.n = 1` through a receiver whose
// mutability the caller picks, so one table exercises the mutable and immutable halves of
// the same rule.
//
// An inline object annotation and a `mut self` receiver already carried mutability into a
// field. An alias, an interface, and a class instance did not: an alias failed the object
// assertion in fieldReadBorrow, and a class field never reached that function at all.
var mutReceiverCarriers = []struct {
	name string
	// src takes the receiver's mutability, `mut ` or empty.
	src func(mutability string) string
}{
	{
		name: "inline object annotation",
		src: func(m string) string {
			return "fn go(c: " + m + "{inner: {n: number}}) -> number { c.inner.n = 1  return 0 }"
		},
	},
	{
		name: "type alias to an object",
		src: func(m string) string {
			return `
				type Config = {inner: {n: number}}
				fn go(c: ` + m + `Config) -> number { c.inner.n = 1  return 0 }`
		},
	},
	{
		name: "declare interface",
		src: func(m string) string {
			return `
				export declare interface Config { inner: {n: number} }
				fn go(c: ` + m + `Config) -> number { c.inner.n = 1  return 0 }`
		},
	},
	{
		name: "class instance",
		src: func(m string) string {
			return `
				class Box {
					inner: {n: number},
					constructor(mut self, inner: {n: number}) { self.inner = inner },
				}
				fn go(b: ` + m + `Box) -> number { b.inner.n = 1  return 0 }`
		},
	},
	{
		name: "class body through the receiver",
		src: func(m string) string {
			return `
				class Box {
					inner: {n: number},
					constructor(mut self, inner: {n: number}) { self.inner = inner },
					grow(` + m + `self) { self.inner.n = 1 },
				}`
		},
	},
}

// A `mut` receiver lends its mutability to a field, so a write through that field is
// legal however the receiver's type is spelled.
func TestMutReceiverReachesAFieldThroughEveryCarrier(t *testing.T) {
	for _, tc := range mutReceiverCarriers {
		t.Run(tc.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src("mut "))
			require.Empty(t, messagesWithSpan(t, errs))
		})
	}
}

// An immutable receiver lends nothing, so the same write is rejected. This is the half
// that makes the rule a permission rather than a default, and it held for every carrier
// before the mutable half did.
func TestImmutableReceiverRejectsAFieldWriteThroughEveryCarrier(t *testing.T) {
	const want = "cannot constrain immutable object <: mutable object"
	for _, tc := range mutReceiverCarriers {
		t.Run(tc.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src(""))
			require.Equal(t, []string{want}, errorMessagesOf(errs))
		})
	}
}

// Mutability carries through each link of a chained read, not just the first, so
// `c.a.b.n = 1` is legal through a `mut` receiver and rejected through an immutable one.
func TestMutReceiverReachesAChainedField(t *testing.T) {
	const src = `
		class Inner {
			b: {n: number},
			constructor(mut self, b: {n: number}) { self.b = b },
		}
		type Outer = {a: Inner}
		fn go(c: %sOuter) -> number { c.a.b.n = 1  return 0 }`
	t.Run("mut receiver", func(t *testing.T) {
		_, _, errs := inferSource(t, fmt.Sprintf(src, "mut "))
		require.Empty(t, messagesWithSpan(t, errs))
	})
	t.Run("immutable receiver", func(t *testing.T) {
		_, _, errs := inferSource(t, fmt.Sprintf(src, ""))
		require.Equal(t,
			[]string{"cannot constrain immutable object <: mutable object"},
			errorMessagesOf(errs))
	})
}

// A field the source marked `mut` keeps its mutability under an immutable receiver. That
// is interior mutability: the receiver decides for a field that did not ask, and this
// field asked. Only a class body can write the shape, #779 rejecting it on an object type
// annotation.
func TestAnExplicitMutFieldSurvivesAnImmutableReceiver(t *testing.T) {
	const src = `
		class Cell { n: number }
		class Holder { readonly inner: mut Cell }
		fn poke(h: %sHolder) -> number { h.inner.n = 1  return 0 }`
	for _, mutability := range []string{"mut ", ""} {
		t.Run("receiver "+mutability, func(t *testing.T) {
			_, _, errs := inferSource(t, fmt.Sprintf(src, mutability))
			require.Empty(t, messagesWithSpan(t, errs))
		})
	}
}

// Routing a class field read through the structural path keeps what the projected-member
// path did for it: the field's type still resolves at the instance's type arguments, and a
// field no class in the chain declares still reports the miss.
func TestAClassFieldReadKeepsItsProjectionAndItsMiss(t *testing.T) {
	t.Run("a generic field projects to the instance's argument", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Box<T> {
				inner: T,
				constructor(mut self, inner: T) { self.inner = inner },
			}
			val b = Box({n: 1})
			val r = b.inner`)
		require.Empty(t, messagesWithSpan(t, errs))
		require.Equal(t, "{n: 1}", values["r"])
	})
	t.Run("an inherited field reads through a subclass", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Animal {
				name: string,
				constructor(mut self, name: string) { self.name = name },
			}
			class Dog extends Animal {
				constructor(mut self, name: string) { super(name) },
			}
			val d = Dog("rex")
			val r = d.name`)
		require.Empty(t, messagesWithSpan(t, errs))
		require.Equal(t, "string", values["r"])
	})
	t.Run("a field no class declares still reports", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Box {
				n: number,
				constructor(mut self, n: number) { self.n = n },
			}
			val b = Box(1)
			val r = b.nope`)
		require.Equal(t,
			[]string{"object is missing property: nope"},
			errorMessagesOf(errs))
	})
}

// --- #1558: a `mut self` member needs a mutable receiver ---

// A method declaring `mut self` needs mutable access to the instance, and the receiver it
// is reached through decides whether there is any. The check runs on an instance reached
// from outside the class as much as on the `self` a body reads, so `c.bump()` and
// `self.bump()` answer the same way for the same receiver.
func TestMutSelfMethodNeedsAMutableReceiver(t *testing.T) {
	const items = `
		class Items {
			n: number,
			constructor(mut self, n: number) { self.n = n },
			bump(mut self) { self.n = 1 },
			read(self) -> number { return self.n },
		}
`
	const want = "cannot constrain immutable Items <: mutable Items"
	tests := []struct {
		name string
		src  string
		errs []string
	}{
		{
			name: "a `mut` parameter lends",
			src:  items + `fn go(i: mut Items) -> number { i.bump()  return 0 }`,
		},
		{
			name: "an immutable parameter does not",
			src:  items + `fn go(i: Items) -> number { i.bump()  return 0 }`,
			errs: []string{want},
		},
		{
			name: "a plain method needs nothing",
			src:  items + `fn go(i: Items) -> number { return i.read() }`,
		},
		{
			name: "a `val mut` binding of a constructor call lends",
			src:  items + `fn go() -> number { val mut i = Items(1)  i.bump()  return 0 }`,
		},
		{
			name: "a plain `val` binding of the same call does not",
			src:  items + `fn go() -> number { val i = Items(1)  i.bump()  return 0 }`,
			errs: []string{want},
		},
		{
			name: "a `mut` receiver lends through a field",
			src: items + `
				class Config {
					items: Items,
					constructor(mut self, items: Items) { self.items = items },
				}
				fn go(c: mut Config) -> number { c.items.bump()  return 0 }`,
		},
		{
			name: "an immutable receiver does not lend through a field",
			src: items + `
				class Config {
					items: Items,
					constructor(mut self, items: Items) { self.items = items },
				}
				fn go(c: Config) -> number { c.items.bump()  return 0 }`,
			errs: []string{want},
		},
		{
			name: "`mut self` inside a body lends to a field's method",
			src: items + `
				class Sub {
					it: Items,
					constructor(mut self, it: Items) { self.it = it },
					go(mut self) { self.it.bump() },
				}`,
		},
		{
			name: "plain `self` inside a body does not",
			src: items + `
				class Sub {
					it: Items,
					constructor(mut self, it: Items) { self.it = it },
					go(self) { self.it.bump() },
				}`,
			errs: []string{want},
		},
		{
			name: "an inherited `mut self` method is checked at the subclass",
			src: `
				class Base {
					n: number,
					constructor(mut self, n: number) { self.n = n },
					bump(mut self) { self.n = 1 },
				}
				class Derived extends Base {
					constructor(mut self) { super(0) },
				}
				fn go(d: Derived) -> number { d.bump()  return 0 }`,
			errs: []string{"cannot constrain immutable Base <: mutable Base"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			if tt.errs == nil {
				require.Empty(t, messagesWithSpan(t, errs))
				return
			}
			require.Equal(t, tt.errs, errorMessagesOf(errs))
		})
	}
}

// Every arm of an overloaded `mut self` method needs the same mutable receiver, since the
// arms share one member and the check reads the receiver before an arm is chosen.
func TestOverloadedMutSelfMethodNeedsAMutableReceiver(t *testing.T) {
	const src = `
		class C {
			n: number,
			constructor(mut self, n: number) { self.n = n },
			f(mut self, x: number) -> number { return x },
			f(mut self, x: string) -> string { return x },
		}
		val %s c = C(0)
		val r = c.f(1)
	`
	t.Run("a `val mut` receiver lends", func(t *testing.T) {
		values, _, errs := inferSource(t, fmt.Sprintf(src, "mut"))
		require.Empty(t, messagesWithSpan(t, errs))
		require.Equal(t, "number", values["r"])
	})
	t.Run("a plain `val` receiver does not", func(t *testing.T) {
		_, _, errs := inferSource(t, fmt.Sprintf(src, ""))
		require.Equal(t,
			[]string{"cannot constrain immutable C <: mutable C"},
			errorMessagesOf(errs))
	})
}

// An owned return type means the caller holds the only reference to the result, so a
// `val mut` binding of a call upgrades it to an owned-mutable value. A borrow return says
// the opposite — the callee kept the value and lent it — so that one does not upgrade.
//
// The result reaches the binding as a variable carrying the return among its lower bounds,
// so the borrow test runs at every level of that walk. Peeling the reference first would
// read `&Counter` as an owned `Counter` and hand the binding exclusive mutable access to a
// value someone else still holds.
func TestValMutUpgradesAnOwnedCallResult(t *testing.T) {
	const counter = `
		class Counter {
			n: number,
			constructor(mut self, n: number) { self.n = n },
			bump(mut self) { self.n = 1 },
		}
`
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a constructor call",
			src:  counter + "val mut d = Counter(0)",
			want: "mut Counter",
		},
		{
			name: "a declared function returning an owned instance",
			src:  counter + "declare fn shared() -> Counter\nval mut d = shared()",
			want: "mut Counter",
		},
		{
			name: "a factory returning a fresh instance",
			src:  counter + "fn make() -> Counter { return Counter(0) }\nval mut d = make()",
			want: "mut Counter",
		},
		{
			name: "a static factory",
			src:  counter + "class F { static of() -> Counter { return Counter(0) }, }\nval mut d = F.of()",
			want: "mut Counter",
		},
		{
			name: "a declared function already returning `mut`",
			src:  counter + "declare fn made() -> mut Counter\nval mut d = made()",
			want: "mut Counter",
		},
		{
			name: "an owned object return",
			src:  "declare fn obj() -> {x: number}\nval mut d = obj()",
			want: "mut {x: number}",
		},
		{
			name: "an owned tuple return",
			src:  "declare fn tup() -> [number, number]\nval mut d = tup()",
			want: "mut [number, number]",
		},
		{
			name: "an immutable borrow return keeps its borrow",
			src:  counter + "declare fn peek() -> &Counter\nval mut d = peek()",
			want: "&Counter",
		},
		{
			name: "a mutable borrow return keeps its borrow",
			src:  counter + "declare fn peek() -> &mut Counter\nval mut d = peek()",
			want: "&mut Counter",
		},
		{
			name: "a primitive return has nothing to make mutable",
			src:  "declare fn num() -> number\nval mut d = num()",
			want: "number",
		},
		{
			name: "a plain `val` binding does not upgrade",
			src:  counter + "declare fn shared() -> Counter\nval d = shared()",
			want: "Counter",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, messagesWithSpan(t, errs))
			require.Equal(t, tt.want, values["d"])
		})
	}
}

// The shape the committed stdlib tree writes, and the case #1554 needs before it can drop
// the `mut` its fields carry: an interface whose field is a generic class carrying a
// `mut self` mutator. `web:web_rtc` declares `certificates?: mut Array<RTCCertificate>`,
// and `std:array` declares `push(mut self, ...items: mut Array<T>) -> number`.
//
// `config.certificates.push(cert)` exercises both halves of this change at once. The
// receiver's mutability has to reach the field, which is part 1, and then satisfy `push`'s
// own `mut self`, which is part 2. The carrier table above only writes a field, so it
// stops short of the second half.
//
// A read and a `self` method stay reachable through an immutable receiver, since neither
// asks for mutable access. `List` stands in for `Array` so the case does not depend on the
// prelude, and the field is written without `mut` because that is the spelling #1554 moves
// the tree to; inside an interface the two behave alike, #779 stripping the `mut`.
func TestMutSelfMethodOnAGenericFieldOfAnInterface(t *testing.T) {
	const decls = `
		export declare class RTCCertificate { expires: number }
		export declare class List<T> {
			length: number,
			push(mut self, item: T) -> number,
			at(self, index: number) -> T,
		}
		export declare interface RTCConfiguration {
			certificates: List<RTCCertificate>,
		}
`
	tests := []struct {
		name string
		src  string
		errs []string
	}{
		{
			name: "a mutator through a `mut` receiver",
			src: decls + `fn go(config: mut RTCConfiguration, cert: RTCCertificate) -> number {
				return config.certificates.push(cert)
			}`,
		},
		{
			name: "a mutator through an immutable receiver",
			src: decls + `fn go(config: RTCConfiguration, cert: RTCCertificate) -> number {
				return config.certificates.push(cert)
			}`,
			errs: []string{
				"cannot constrain immutable List<RTCCertificate> <: mutable List<RTCCertificate>",
			},
		},
		{
			name: "a field read through an immutable receiver",
			src:  decls + `fn go(config: RTCConfiguration) -> number { return config.certificates.length }`,
		},
		{
			name: "a `self` method through an immutable receiver",
			src: decls + `fn go(config: RTCConfiguration) -> RTCCertificate {
				return config.certificates.at(0)
			}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			if tt.errs == nil {
				require.Empty(t, messagesWithSpan(t, errs))
				return
			}
			require.Equal(t, tt.errs, errorMessagesOf(errs))
		})
	}
}

// A `mut self` method is reachable on an owned call result bound with `val mut`, which is
// the point of the upgrade above: a factory is how a class with validation or a private
// constructor is written, and its result would otherwise be unusable.
func TestMutSelfMethodReachableOnAnOwnedCallResult(t *testing.T) {
	const src = `
		class Counter {
			n: number,
			constructor(mut self, n: number) { self.n = n },
			bump(mut self) { self.n = 1 },
		}
		fn make() -> Counter { return Counter(0) }
		fn go() -> number { val %s d = make()  d.bump()  return 0 }`
	t.Run("a `val mut` binding reaches it", func(t *testing.T) {
		_, _, errs := inferSource(t, fmt.Sprintf(src, "mut"))
		require.Empty(t, messagesWithSpan(t, errs))
	})
	t.Run("a plain `val` binding does not", func(t *testing.T) {
		_, _, errs := inferSource(t, fmt.Sprintf(src, ""))
		require.Equal(t,
			[]string{"cannot constrain immutable Counter <: mutable Counter"},
			errorMessagesOf(errs))
	})
}
