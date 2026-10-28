package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M4 C3: field-write inference + read-after-write ---
//
// A field write `recv.prop = source` extends inferAssign's member-target branch. Through a
// `mut` binding it constrains `recv <: {prop: widen(source), ...}`, an inexact one-property
// requirement, and the C3 coalesce fold collapses every selection on the receiver, reads and
// writes alike, into one object. The stored value is widened (5 ⇒ number) because the field
// may be written again. These tests exercise the feature end to end through the inferred
// signatures of unannotated `mut` parameters.

// Two writes on an inferred param fold into a single object: each write contributes a
// one-property requirement, and the fold unions them.
func TestInferMemberAssignTwoWrites(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(mut obj) { obj.x = 5
 obj.y = 10 }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: {x: number, y: number}) -> undefined", values["foo"])
}

// Read-after-write: a read of a field just written to the same receiver returns the
// recorded concrete (widened) type, not a fresh var, so `obj.x = 5; return obj.x` is
// `number`. The receiver renders `{x: number}` from the single write.
func TestInferMemberAssignReadAfterWrite(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(mut obj) { obj.x = 5
 return obj.x }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: {x: number}) -> number", values["foo"])
}

// A mixed read and write folds into ONE object rather than an intersection of a read and a
// write requirement. The caller moves the argument in, so the read-only `bar` stays
// covariant, and its unused var is inlined to `unknown`. The written `baz` is the widened
// `number`.
func TestInferMemberAssignMixedReadWrite(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(mut obj) { val x = obj.bar
 obj.baz = 5 }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: {bar: unknown, baz: number}) -> undefined", values["foo"])
}

// A compound written value widens recursively via the shared widen (B3): writing
// `{x: 0}` stores `{x: number}`, not the literal `{x: 0}`.
func TestInferMemberAssignCompoundValueWidens(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(mut obj) { obj.p = {x: 0} }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: {p: {x: number}}) -> undefined", values["foo"])
}

// The assignment expression evaluates to the value just stored, so its type is the
// widened source: `val r = (obj.x = 5)` ⇒ r: number, used inside a function so the
// receiver is an inferable place.
func TestInferMemberAssignValueIsStored(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(mut obj) { val r = (obj.x = 5)
 return r }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: {x: number}) -> number", values["foo"])
}

// Writing different literal values to the same field is the same widened primitive,
// so the field stays `number` (no contradiction between `5` and `10`).
func TestInferMemberAssignSameFieldTwice(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(mut obj) { obj.x = 5
 obj.x = 10 }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: {x: number}) -> undefined", values["foo"])
}

// A written field and a read-only field that ESCAPES (is returned) fold into one
// object: the written `x` is `number`, while the returned `y` becomes a real type
// parameter rather than collapsing to `unknown`, because it occurs in an output position.
// This is the key interplay between the C3 merge and generalization.
func TestInferMemberAssignWrittenAndEscapingReadField(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(mut obj) { obj.x = 5
 return obj.y }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(obj: {x: number, y: T0}) -> T0", values["foo"])
}

// Write-after-read on the SAME field needs no `written`-map support: the read mints
// `T0` and constrains `obj <: {x: T0}`, the later write adds `obj <: {x: number}`,
// and the two upper bounds merge so the field folds to `T0 & number`. The read's
// value (returned `x`) stays `T0`. This asserts the plan's claim that write-after-read
// falls out of ordinary constraint accumulation, the reverse of read-after-write.
func TestInferMemberAssignWriteAfterRead(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(mut obj) { val x = obj.x
 obj.x = 5
 return x }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(obj: {x: T0 & number}) -> T0", values["foo"])
}

// A write through a nested receiver needs only the `mut` on the root binding: every owned
// layer of a `mut` binding's value is writable, so `obj.p.x = 5` infers `{p: {x: number}}`
// with no mutability in the type, and the displayed signature round-trips.
func TestInferMemberAssignNestedReceiver(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(mut obj) { obj.p.x = 5 }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: {p: {x: number}}) -> undefined", values["foo"])
}

// An `open` param's written object stays row-polymorphic: the C3 fold passes the
// var's Open flag to mergeObjectGroup, so the merged object is inexact and callers
// may pass an object with extra fields.
func TestInferMemberAssignOpenParam(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(open mut obj) { obj.x = 5 }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: {x: number, ...}) -> undefined", values["foo"])
}

// A written receiver that ESCAPES (the whole object is returned) is not sealed: it
// occurs in an output position, so the param keeps an open row and renders as a type
// parameter bounded by the written requirement.
func TestInferMemberAssignWrittenObjectEscapes(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(mut obj) { obj.x = 5
 return obj }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0: {x: number, ...}>(obj: T0) -> T0", values["foo"])
}

// Writing a parameter's value into a field LINKS their types (#737). The write
// `obj.x = v` makes the field's type the variable `v`, and `v` occurs in BOTH
// polarities, so single-polarity elimination retains it as a shared type parameter
// instead of inlining each occurrence to `unknown`. So `fn foo(mut obj, v) { obj.x = v }` infers the tighter
// `fn <T0>(obj: {x: T0}, v: T0) -> undefined`. The mut-field invariance reaches the
// occurrence analysis via recordMutWriteView (simplify.go).
func TestInferMemberAssignVariableValueLinked(t *testing.T) {
	values, _, errs := inferSource(t, "fn foo(mut obj, v) { obj.x = v }")
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(obj: {x: T0}, v: T0) -> undefined", values["foo"])
}

// Writing one field of a concretely-typed (annotated) `mut` parameter checks: the field
// write lowers to the inexact requirement `{x, ...}`, which tolerates the object's other
// declared fields.
//
// The signature carries the parameter's plain type, so it renders as `obj: {x: number, y: string}`.
func TestInferMemberAssignAnnotatedMutObject(t *testing.T) {
	values, _, errs := inferSource(t, "fn f(mut obj: {x: number, y: string}) { obj.x = 5 }")
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: {x: number, y: string}) -> undefined", values["f"])
}

// A write into a declared field is checked against the field's type, so storing a string
// into a number field of an annotated mut object reports one error, blamed on the value.
func TestInferMemberAssignAnnotatedMutWrongType(t *testing.T) {
	_, _, errs := inferSource(t, "fn f(mut obj: {x: number, y: string}) { obj.x = \"bad\" }")
	require.Equal(t, []string{
		`1:49-1:54: cannot constrain "bad" <: number`,
	}, messagesWithSpan(t, errs))
}

// A write into a declared field checks whenever the value fits the field's type. The field's
// type may be wider than the value, as a union, an optional field or a literal union is, and
// the receiver may be an object, a class instance or a borrow. A value outside the field's
// type is still rejected, with one error blamed on the value. A write that resolves to a
// setter takes the same rule against the setter's parameter, through its own path.
func TestInferMemberAssignIntoAWiderDeclaredField(t *testing.T) {
	setter := func(param string) string {
		return "class C {\n_v: number,\nconstructor(&mut self) { self._v = 0 },\nget v(&self) -> number { return self._v },\nset v(&mut self, x: " + param + ") { self._v = 0 },\n}\n"
	}
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "a member of a union field",
			src:  `fn go(mut d: {x: number | string}) { d.x = "s" }`,
		},
		{
			name: "the other member of a union field",
			src:  `fn go(mut d: {x: number | string}) { d.x = 5 }`,
		},
		{
			name: "undefined into a union with undefined",
			src:  `fn go(mut d: {x: string | undefined}) { d.x = undefined }`,
		},
		{
			name: "a value into an optional field",
			src:  `fn go(mut d: {x?: string}) { d.x = "s" }`,
		},
		{
			name: "undefined into an optional field",
			src:  `fn go(mut d: {x?: string}) { d.x = undefined }`,
		},
		{
			name: "a member of a literal union field",
			src:  `fn go(mut d: {x: "a" | "b"}) { d.x = "a" }`,
		},
		{
			name: "through a mutable borrow",
			src:  `fn go(d: &mut {x: number | string}) { d.x = "s" }`,
		},
		{
			name: "into a class instance's union field",
			src:  "class C { x: number | string }\nfn go(mut c: C) { c.x = \"s\" }",
		},
		{
			name: "into an annotated binding's union field",
			src:  "fn go() { val mut d: {x: number | string} = {x: 1}\nd.x = \"s\" }",
		},
		{
			name: "a member of a setter's union parameter",
			src:  setter("number | string") + `fn go(mut c: C) { c.v = "s" }`,
		},
		{
			name: "a member of a setter's literal union parameter",
			src:  setter(`"a" | "b"`) + `fn go(mut c: C) { c.v = "a" }`,
		},
		{
			name:    "a value outside a union field",
			src:     `fn go(mut d: {x: number | string}) { d.x = true }`,
			wantErr: "cannot constrain true <: number | string",
		},
		{
			name:    "a value outside a literal union field",
			src:     `fn go(mut d: {x: "a" | "b"}) { d.x = "c" }`,
			wantErr: `cannot constrain "c" <: "a" | "b"`,
		},
		{
			name:    "a value outside a setter's union parameter",
			src:     setter("number | string") + `fn go(mut c: C) { c.v = true }`,
			wantErr: "cannot constrain true <: number | string",
		},
		{
			name:    "a value outside a setter's literal union parameter",
			src:     setter(`"a" | "b"`) + `fn go(mut c: C) { c.v = "c" }`,
			wantErr: `cannot constrain "c" <: "a" | "b"`,
		},
		{
			name:    "an immutable receiver",
			src:     `fn go(d: {x: number | string}) { d.x = "s" }`,
			wantErr: "cannot mutate 'd' because its binding is immutable; declare it as `mut d`",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			if tt.wantErr == "" {
				require.Empty(t, errorMessagesOf(errs))
				return
			}
			require.Equal(t, []string{tt.wantErr}, errorMessagesOf(errs))
		})
	}
}

// Writing a field absent from an EXACT annotated mut object still errors: the read
// view demands the object carry the written field.
func TestInferMemberAssignAnnotatedMutMissingField(t *testing.T) {
	src := "fn f(mut obj: {x: number}) { obj.z = 5 }"
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{"1:15-1:26: object is missing property: z"}, messagesWithSpan(t, errs))
}

// KNOWN GAP: two writes of INCOMPATIBLE types to one field produce an uninhabited
// `number & string` rather than an error — each write is an independent upper bound
// on the receiver var with no constraint relating them. Pinned so the gap is
// explicit; a future soundness pass over conflicting writes should surface an error
// here and update this assertion.
// TODO(#738): report conflicting writes to one field instead of folding to an
// uninhabited intersection.
func TestInferMemberAssignConflictingWritesNoError(t *testing.T) {
	values, _, errs := inferSource(t, `fn foo(mut obj) { obj.x = 5
 obj.x = "hi" }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: {x: number & string}) -> undefined", values["foo"])
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
				fn f(mut c: C) { c.v = 5 }
			`,
		},
		{
			// A class receiver's field is declared, as an object receiver's is, so a
			// string into a number field is checked against the field's type and
			// reports one error on the value.
			name: "mut receiver rejects a wrongly-typed write",
			src: `
				class C { v: number }
				fn f(mut c: C) { c.v = "bad" }
			`,
			want: []string{
				`3:28-3:33: cannot constrain "bad" <: number`,
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
			want: []string{"3:18-3:25: cannot mutate 'c' because its binding is immutable; declare it as `mut c`"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
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
			src:  `class C { v: number, set x(&mut self, n: number) { self.v = n }, m(&mut self) { self.x = 5 } }`,
		},
		{
			name: "mut instance receiver",
			src: `
				class C { v: number, set x(&mut self, n: number) { self.v = n } }
				fn f(mut c: C) { c.x = 5 }
			`,
		},
		{
			name: "immutable instance receiver has no mut to lend",
			src: `
				class C { v: number, set x(&mut self, n: number) { self.v = n } }
				fn f(c: C) { c.x = 5 }
			`,
			want: []string{"3:18-3:21: cannot mutate 'c' because its binding is immutable; declare it as `mut c`"},
		},
		{
			// A setter mutates the instance, so a plain `self` receiver is rejected at the
			// declaration. The write itself draws nothing further, since the elem keeps the
			// declared receiver and the diagnostic belongs to the declaration.
			name: "plain self setter is rejected at its declaration",
			src: `
				class C { set x(&self, n: number) { } }
				fn f(c: C) { c.x = 5 }
			`,
			want: []string{"2:15-2:42: Setter 'x' must declare a `&mut self` receiver; writing through it mutates the instance."},
		},
		{
			name: "mut self setter reached from a plain self body",
			src:  `class C { v: number, set x(&mut self, n: number) { self.v = n }, m(&self) { self.x = 5 } }`,
			want: []string{"1:77-1:83: cannot constrain immutable C <: mutable C"},
		},
		{
			name: "value is checked against the setter parameter",
			src: `
				class C { v: number, set x(&mut self, n: number) { self.v = n } }
				fn f(mut c: C) { c.x = "hi" }
			`,
			want: []string{`3:28-3:32: cannot constrain "hi" <: number`},
		},
		{
			name: "inherited setter resolves through a subclass instance",
			src: `
				class C { v: number, set x(&mut self, n: number) { self.v = n } }
				class D extends C { constructor(&mut self) { super(0) } }
				fn f(mut d: D) { d.x = 5 }
			`,
		},
		{
			name: "setter write inside the class's own constructor",
			src: `
				class C {
					v: number,
					constructor(&mut self) { self.v = 0 self.x = 5 },
					set x(&mut self, n: number) { self.v = n },
				}
			`,
		},
		{
			name: "generic setter parameter projects the instance argument",
			src: `
				class Box<T> { v: T, set x(&mut self, n: T) { self.v = n } }
				fn f(mut b: Box<number>) { b.x = "hi" }
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
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
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
					set x(&mut self, n: number) { self.v = n },
					get x(&self) -> number { return self.v },
				}
				fn f(mut c: C) -> number {
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
					get x(&self) -> number { return self.v },
					set x(&mut self, n: number) { self.v = n },
				}
				fn f(mut c: C) -> number {
					c.x = 5
					return c.x
				}
			`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Empty(t, messagesWithSpan(t, errs))
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
				class C { v: number, get x(&self) -> number { return self.v } }
				fn f(mut c: C) { c.x = 5 }
			`,
			want: []string{"3:22-3:29: Property 'x' is read-only; it has a getter but no setter or field to write."},
		},
		{
			name: "self receiver",
			src:  `class C { v: number, get x(&self) -> number { return self.v }, m(&mut self) { self.x = 5 } }`,
			want: []string{"1:79-1:89: Property 'x' is read-only; it has a getter but no setter or field to write."},
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
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
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
			get x(&self) -> string { return "s" },
			set x(&mut self, n: number) { self.v = n },
		}
		fn written(mut c: C) { return (c.x = 5) }
		fn readBack(mut c: C) { c.x = 5
			return c.x
		}
	`)
	require.Empty(t, messagesWithSpan(t, errs))
	require.Equal(t, "fn (c: C) -> number", values["written"])
	require.Equal(t, "fn (c: C) -> string", values["readBack"])
}

// A `&mut` borrow reaches a receiver position through a call result or a branch join as a
// variable carrying the borrow among its lower bounds, not as a bare RefType. The setter
// write looks through that variable for the receiver's mutability, so it accepts the same
// receivers a field write and a `&mut self` method call accept. An immutable receiver is
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
				class C { v: number, set x(&mut self, n: number) { self.v = n } }
				declare fn g(c: &mut C) -> &mut C
				fn f(mut c: C) { g(&mut c).x = 5 }
			`,
		},
		{
			name: "branch join",
			src: `
				class C { v: number, set x(&mut self, n: number) { self.v = n } }
				fn f(a: &mut C, b: &mut C, cond: boolean) { val r = if (cond) { a } else { b }
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
				class C { v: number, set x(&mut self, n: number) { self.v = n } }
				fn f(a: &mut C, b: &C, cond: boolean) { val r = if (cond) { a } else { b }
					r.x = 5
				}
			`,
			want: []string{"4:6-4:9: cannot constrain immutable C <: mutable C"},
		},
		{
			// A call result is a temporary no binding owns, so nothing makes it mutable.
			name: "immutable call result",
			src: `
				class C { v: number, set x(&mut self, n: number) { self.v = n } }
				declare fn g(c: C) -> C
				fn f(c: C) { g(c).x = 5 }
			`,
			want: []string{"4:18-4:24: cannot constrain immutable C <: mutable C"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}
