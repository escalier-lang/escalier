package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M4 C3: field-write inference + read-after-write ---
//
// A field write `recv.prop = source` extends inferAssign's member-target branch. It
// constrains `recv <: mut {prop: widen(source), ...}` — a mutable, inexact
// one-property requirement — and the C3 coalesce fold collapses every selection on
// the receiver (reads and writes) into one `mut` object once any field is written.
// The stored value is widened (5 ⇒ number) because writing through a `mut` receiver
// is itself a mutation. These tests exercise the feature end to end through inferred
// function signatures.

// Two writes on an inferred param fold into a single `mut` object: each write
// contributes a mutable one-property requirement, and the fold unions them and wraps
// the whole object in `mut`.
func TestInferMemberAssignTwoWrites(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(obj) { obj.x = 5
 obj.y = 10 }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {x: number, y: number}) -> undefined", values["foo"])
}

// Read-after-write: a read of a field just written to the same receiver returns the
// recorded concrete (widened) type, not a fresh var, so `obj.x = 5; return obj.x` is
// `number`. The receiver renders `mut {x: number}` from the single write.
func TestInferMemberAssignReadAfterWrite(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(obj) { obj.x = 5
 return obj.x }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {x: number}) -> number", values["foo"])
}

// A mixed read and write folds into ONE `mut` object rather than the spike's
// `{bar: …} & mut {baz: …}` intersection: the presence of any write makes every
// selection — the read-only `bar` included — fold into the mutable object. Folding
// `bar` into the `mut` object makes it invariant (#737), so its var occurs in both
// polarities and is retained as a type parameter `T0` the caller picks, rather than
// inlined to `unknown`. The written `baz` is the widened `number`.
func TestInferMemberAssignMixedReadWrite(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(obj) { val x = obj.bar
 obj.baz = 5 }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(obj: mut {bar: T0, baz: number}) -> undefined", values["foo"])
}

// A compound written value widens recursively via the shared widen (B3): writing
// `{x: 0}` stores `{x: number}`, not the literal `{x: 0}`.
func TestInferMemberAssignCompoundValueWidens(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(obj) { obj.p = {x: 0} }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {p: {x: number}}) -> undefined", values["foo"])
}

// The assignment expression evaluates to the value just stored, so its type is the
// widened source: `val r = (obj.x = 5)` ⇒ r: number, used inside a function so the
// receiver is an inferable place.
func TestInferMemberAssignValueIsStored(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(obj) { val r = (obj.x = 5)
 return r }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {x: number}) -> number", values["foo"])
}

// Writing different literal values to the same field is the same widened primitive,
// so the field stays `number` (no contradiction between `5` and `10`).
func TestInferMemberAssignSameFieldTwice(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(obj) { obj.x = 5
 obj.x = 10 }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {x: number}) -> undefined", values["foo"])
}

// A written field and a read-only field that ESCAPES (is returned) fold into one
// `mut` object: the written `x` is `number`, while the returned `y` becomes a real
// type parameter rather than collapsing to `unknown`, because it occurs in an output
// position. This is the key interplay between the C3 mut-merge and generalization.
func TestInferMemberAssignWrittenAndEscapingReadField(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(obj) { obj.x = 5
 return obj.y }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(obj: mut {x: number, y: T0}) -> T0", values["foo"])
}

// Write-after-read on the SAME field needs no `written`-map support: the read mints
// `T0` and constrains `obj <: {x: T0}`, the later write adds `obj <: mut {x: number}`,
// and the two upper bounds merge so the field folds to `T0 & number`. The read's
// value (returned `x`) stays `T0`. This pins the plan's claim that write-after-read
// falls out of ordinary constraint accumulation, the reverse of read-after-write.
func TestInferMemberAssignWriteAfterRead(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(obj) { val x = obj.x
 obj.x = 5
 return x }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(obj: mut {x: T0 & number}) -> T0", values["foo"])
}

// A write through a nested receiver marks the WHOLE container `mut` (#779): writing
// `obj.p.x` makes `obj` itself mutable rather than nesting an owned-mut cell on the
// `p` field. `mut` is deep, so `mut {p: {x: number}}` already makes `p.x` writable,
// and unlike the rejected `{p: mut {x: number}}` it is a valid annotation — the
// displayed signature round-trips. The cost is precision: a caller must pass a mutable
// container even though only the nested field is written.
func TestInferMemberAssignNestedReceiver(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(obj) { obj.p.x = 5 }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {p: {x: number}}) -> undefined", values["foo"])
}

// An `open` param's written object stays row-polymorphic: the C3 fold passes the
// var's Open flag to mergeObjectGroup, so the merged `mut` object is inexact and
// callers may pass an object with extra fields.
func TestInferMemberAssignOpenParam(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(open obj) { obj.x = 5 }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {x: number, ...}) -> undefined", values["foo"])
}

// A written receiver that ESCAPES (the whole object is returned) is not sealed: it
// occurs in an output position, so the param keeps an open row and renders as the
// written requirement intersected with the returned type parameter.
func TestInferMemberAssignWrittenObjectEscapes(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(obj) { obj.x = 5
 return obj }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(obj: T0 & mut {x: number}) -> T0", values["foo"])
}

// Writing a parameter's value into a field LINKS their types (#737). The write
// `obj.x = v` makes the field's type the variable `v`, and because the field is
// `mut` — hence invariant — `v` occurs in BOTH polarities, so single-polarity
// elimination retains it as a shared type parameter instead of inlining each
// occurrence to `unknown`. So `fn foo(obj, v) { obj.x = v }` infers the tighter
// `fn <T0>(obj: mut {x: T0}, v: T0) -> undefined`. The mut-field invariance reaches the
// occurrence analysis via recordMutWriteView (simplify.go).
func TestInferMemberAssignVariableValueLinked(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(obj, v) { obj.x = v }")
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(obj: mut {x: T0}, v: T0) -> undefined", values["foo"])
}

// Writing one field of a concretely-typed (annotated) mut object checks: the field
// write lowers to the inexact requirement `mut {x, ...}`, and the RefType rule's
// per-field write view pins x invariantly while tolerating the object's other
// declared fields. Before the per-field write view this reported spurious
// "missing property: y" / "inexact <: exact" errors.
//
// The annotated `mut` param originates a borrow lifetime (D2), but it is unused in
// the `undefined` result, so D4's display-time elision drops it and the param renders as
// plain owned-mutable `mut {…}`.
func TestInferMemberAssignAnnotatedMutObject(t *testing.T) {
	values, _, errs := inferSource(t, "fn f(obj: mut {x: number, y: string}) { obj.x = 5 }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {x: number, y: string}) -> undefined", values["f"])
}

// The named field stays invariant: storing a string into a number field of an
// annotated mut object is rejected in both directions (the read view number <:
// string and the write-back string <: number), so the relaxation is width-only.
func TestInferMemberAssignAnnotatedMutWrongType(t *testing.T) {
	_, _, errs := inferSource(t, "fn f(obj: mut {x: number, y: string}) { obj.x = \"bad\" }")
	require.Equal(t, []string{
		"1:41-1:54: cannot constrain number <: string",
		"1:41-1:54: cannot constrain string <: number",
	}, messagesWithSpan(errs))
}

// Writing a field absent from an EXACT annotated mut object still errors: the read
// view demands the object carry the written field.
func TestInferMemberAssignAnnotatedMutMissingField(t *testing.T) {
	src := "fn f(obj: mut {x: number}) { obj.z = 5 }"
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{"1:15-1:26: object is missing property: z"}, messagesWithSpan(errs))
}

// KNOWN GAP: two writes of INCOMPATIBLE types to one field produce an uninhabited
// `number & string` rather than an error — each write is an independent upper bound
// on the receiver var with no constraint relating them. Pinned so the gap is
// explicit; a future soundness pass over conflicting writes should surface an error
// here and update this assertion.
// TODO(#738): report conflicting writes to one field instead of folding to an
// uninhabited intersection.
func TestInferMemberAssignConflictingWritesNoError(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(obj) { obj.x = 5
 obj.x = "hi" }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {x: number & string}) -> undefined", values["foo"])
}

// A field write through a `mut` reference to a CLASS instance projects the class body and
// reuses the object arm's per-field pinning, the same path a `mut` object target takes.
// The write requirement is a synthesized one-property object, which no nominal class
// satisfies structurally, so the class receiver must reach that arm in the forward
// direction only (escalier-lang/escalier#870).
func TestInferMemberAssignClassReceiver(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// The forward direction alone decides this, so the write succeeds.
			name: "mut receiver accepts a well-typed write",
			src: `
				class C { v: number }
				fn f(c: mut C) { c.v = 5 }
			`,
		},
		{
			// The written field stays invariant through a class receiver exactly as it
			// does through an object receiver, so a string into a number field is
			// rejected in both the read view and the write-back.
			name: "mut receiver rejects a wrongly-typed write in both directions",
			src: `
				class C { v: number }
				fn f(c: mut C) { c.v = "bad" }
			`,
			want: []string{
				"3:22-3:33: cannot constrain number <: string",
				"3:22-3:33: cannot constrain string <: number",
			},
		},
		{
			// An IMMUTABLE receiver has no mutable view to lend, so the requirement's
			// `mut` is unsatisfiable however well-typed the value is.
			name: "immutable receiver rejects the write",
			src: `
				class C { v: number }
				fn f(c: C) { c.v = 5 }
			`,
			want: []string{"3:18-3:25: cannot constrain immutable C <: mutable object"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(errs))
		})
	}
}

// A field write whose receiver carries a setter under the written name is a setter call,
// not a field store. It never mints the structural one-property requirement, which
// constrain's object arm resolves with ObjectType.Prop and so cannot match a SetterElem.
// The source is checked against the setter's declared parameter and the receiver against
// the setter's own `self`.
func TestInferMemberAssignSetter(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "self receiver inside a mut self method",
			src:  `class C { v: number, set x(mut self, n: number) { self.v = n }, m(mut self) { self.x = 5 } }`,
		},
		{
			name: "mut instance receiver",
			src: `
				class C { v: number, set x(mut self, n: number) { self.v = n } }
				fn f(c: mut C) { c.x = 5 }
			`,
		},
		{
			name: "immutable instance receiver has no mut to lend",
			src: `
				class C { v: number, set x(mut self, n: number) { self.v = n } }
				fn f(c: C) { c.x = 5 }
			`,
			want: []string{"3:18-3:21: cannot constrain immutable C <: mutable C"},
		},
		{
			// A setter mutates the instance, so a plain `self` receiver is rejected at the
			// declaration. The write itself draws nothing further, since the elem keeps the
			// declared receiver and the diagnostic belongs to the declaration.
			name: "plain self setter is rejected at its declaration",
			src: `
				class C { set x(self, n: number) { } }
				fn f(c: C) { c.x = 5 }
			`,
			want: []string{"2:15-2:41: Setter 'x' must declare a `mut self` receiver; writing through it mutates the instance."},
		},
		{
			name: "mut self setter reached from a plain self body",
			src:  `class C { v: number, set x(mut self, n: number) { self.v = n }, m(self) { self.x = 5 } }`,
			want: []string{"1:75-1:81: cannot constrain immutable C <: mutable C"},
		},
		{
			name: "value is checked against the setter parameter",
			src: `
				class C { v: number, set x(mut self, n: number) { self.v = n } }
				fn f(c: mut C) { c.x = "hi" }
			`,
			want: []string{`3:28-3:32: cannot constrain "hi" <: number`},
		},
		{
			name: "inherited setter resolves through a subclass instance",
			src: `
				class C { v: number, set x(mut self, n: number) { self.v = n } }
				class D extends C { constructor(mut self) { } }
				fn f(d: mut D) { d.x = 5 }
			`,
		},
		{
			name: "setter write inside the class's own constructor",
			src: `
				class C {
					v: number,
					constructor(mut self) { self.v = 0 self.x = 5 },
					set x(mut self, n: number) { self.v = n },
				}
			`,
		},
		{
			name: "generic setter parameter projects the instance argument",
			src: `
				class Box<T> { v: T, set x(mut self, n: T) { self.v = n } }
				fn f(b: mut Box<number>) { b.x = "hi" }
			`,
			want: []string{`3:38-3:42: cannot constrain "hi" <: number`},
		},
		{
			name: "static setter",
			src: `
				class C { static set x(n: number) { } }
				fn f() { C.x = 5 }
			`,
		},
		{
			name: "static setter rejects a wrongly-typed value",
			src: `
				class C { static set x(n: number) { } }
				fn f() { C.x = "hi" }
			`,
			want: []string{`3:20-3:24: cannot constrain "hi" <: number`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(errs))
		})
	}
}

// A getter and a setter that share a name resolve by direction rather than by declaration
// order: a write takes the setter and a read takes the getter, whichever half comes first
// in the class body. ObjectType.WriteMember and ObjectType.ReadMember are the two lookups
// that make that choice.
func TestInferMemberAssignAccessorPair(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "setter declared first",
			src: `
				class C {
					v: number,
					set x(mut self, n: number) { self.v = n },
					get x(self) -> number { return self.v },
				}
				fn f(c: mut C) -> number {
					c.x = 5
					return c.x
				}
			`,
		},
		{
			name: "getter declared first",
			src: `
				class C {
					v: number,
					get x(self) -> number { return self.v },
					set x(mut self, n: number) { self.v = n },
				}
				fn f(c: mut C) -> number {
					c.x = 5
					return c.x
				}
			`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Empty(t, messagesWithSpan(errs))
		})
	}
}

// A write to a getter-only member has no setter to call. It reports ReadOnlyPropertyError,
// the mirror of the WriteOnlyPropertyError a read of a setter-only member reports.
func TestInferMemberAssignGetterOnly(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "instance receiver",
			src: `
				class C { v: number, get x(self) -> number { return self.v } }
				fn f(c: mut C) { c.x = 5 }
			`,
			want: []string{"3:22-3:29: Property 'x' is read-only; it has a getter but no setter or field to write."},
		},
		{
			name: "self receiver",
			src:  `class C { v: number, get x(self) -> number { return self.v }, m(mut self) { self.x = 5 } }`,
			want: []string{"1:77-1:87: Property 'x' is read-only; it has a getter but no setter or field to write."},
		},
		{
			name: "static receiver",
			src: `
				class C { static get x() -> number { return 1 } }
				fn f() { C.x = 5 }
			`,
			want: []string{"3:14-3:21: Property 'x' is read-only; it has a getter but no setter or field to write."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(errs))
		})
	}
}

// A setter write evaluates to the value just written, widened the way a field write
// widens it, so a caller reads `number` whether `x` is a field or a setter. Nothing is
// recorded in `written`, so a later read of the same name runs the getter rather than
// returning the value just passed in.
func TestInferMemberAssignSetterValue(t *testing.T) {
	values, _, errs := inferSource(t, `
		class C {
			v: number,
			get x(self) -> string { return "s" },
			set x(mut self, n: number) { self.v = n },
		}
		fn written(c: mut C) { return (c.x = 5) }
		fn readBack(c: mut C) { c.x = 5
			return c.x
		}
	`)
	require.Empty(t, messagesWithSpan(errs))
	require.Equal(t, "fn (c: mut C) -> number", values["written"])
	require.Equal(t, "fn (c: mut C) -> string", values["readBack"])
}

// A `mut` borrow reaches a receiver position through a call result or a branch join as a
// variable carrying the borrow among its lower bounds, not as a bare RefType. The setter
// write looks through that variable for the receiver's mutability, so it accepts the same
// receivers a field write and a `mut self` method call accept. An immutable receiver is
// still rejected.
func TestInferMemberAssignSetterIndirectReceiver(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "call result",
			src: `
				class C { v: number, set x(mut self, n: number) { self.v = n } }
				declare fn g(c: mut C) -> mut C
				fn f(c: mut C) { g(c).x = 5 }
			`,
		},
		{
			name: "branch join",
			src: `
				class C { v: number, set x(mut self, n: number) { self.v = n } }
				fn f(a: mut C, b: mut C, cond: boolean) { val r = if (cond) { a } else { b }
					r.x = 5
				}
			`,
		},
		{
			// Every value the receiver may hold has to lend mutable access. A join with an
			// immutable branch does not, since that branch may be the one taken, so it is
			// rejected exactly as the structural field-write path rejects it.
			name: "branch join with one immutable arm",
			src: `
				class C { v: number, set x(mut self, n: number) { self.v = n } }
				fn f(a: mut C, b: C, cond: boolean) { val r = if (cond) { a } else { b }
					r.x = 5
				}
			`,
			want: []string{"4:6-4:9: cannot constrain immutable C <: mutable C"},
		},
		{
			name: "immutable call result",
			src: `
				class C { v: number, set x(mut self, n: number) { self.v = n } }
				declare fn g(c: C) -> C
				fn f(c: C) { g(c).x = 5 }
			`,
			want: []string{"4:18-4:24: cannot constrain immutable C <: mutable C"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(errs))
		})
	}
}
