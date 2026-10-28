package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// typeRuleCase is one per-rule conformance case for a type rule in
// planning/ownership_rules/requirements.md. The rule's name leads the case's name.
type typeRuleCase struct {
	name string
	// src is the Escalier program for the case.
	src string
	// want lists every diagnostic the program reports, each with its span. It is empty when the
	// program checks clean.
	want []string
}

// runTypeRuleCases checks each case's program against the standard library and asserts the full
// list of diagnostics.
func runTypeRuleCases(t *testing.T, cases []typeRuleCase) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, c.src)
			require.Equal(t, c.want, errs)
		})
	}
}

// literalWideningCase is one T9 case. It asserts the type inferred for every top-level value
// binding of a program.
type literalWideningCase struct {
	name string
	src  string
	// want maps each top-level value binding to its inferred type in Escalier type syntax.
	want map[string]string
}

// runLiteralWideningCases infers each case's program, requires it to check clean, and asserts the
// type of every top-level value binding.
func runLiteralWideningCases(t *testing.T, cases []literalWideningCase) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			values, _, errs := inferSource(t, c.src)
			require.Empty(t, errs)
			require.Equal(t, c.want, values)
		})
	}
}

// TestTypeRules covers the per-rule conformance cases for T2, T3, T5, T8, and T10 in
// planning/ownership_rules/requirements.md. Each case copies the rule's example program when the
// rule has one. The cases the current solver gets wrong sit in the disabled tests after
// TestTypeRuleLiteralWidening, grouped by the issue that enables them.
func TestTypeRules(t *testing.T) {
	runTypeRuleCases(t, []typeRuleCase{
		// A value owned by a `val` binding is immutable, so a write to its field is rejected.
		{
			name: "T2/WriteThroughValBinding",
			src: `
				fn g() {
					val p: {x: number} = {x: 0}
					p.x = 1
				}
			`,
			want: []string{"4:6-4:13: cannot constrain immutable object <: mutable object"},
		},
		// A `val mut` binding makes every owned layer writable, so a write two layers down is
		// accepted. The case leaves the type unannotated. TestTypeRulesBindingMutability covers
		// the annotated form.
		{
			name: "T3/DeepWriteThroughValMutBinding",
			src: `
				fn g() {
					val mut p = {a: {b: 0}}
					p.a.b = 1
				}
			`,
		},
		// The array is owned by a `val` binding, so `push` is rejected. Each element is a `&mut`
		// borrow, which carries its own mutability, so the write through `a[0]` is accepted.
		{
			name: "T3/ValArrayOfMutableBorrows",
			src: `
				type Point = {x: number, y: number}
				fn g(q: &mut Point) {
					val a: Array<&mut Point> = []
					a.push(q)
					a[0].x = 1
				}
			`,
			want: []string{"5:6-5:12: cannot constrain immutable Array<&mut Point> <: mutable Array<&mut Point>"},
		},
		// A `mut` inside an owned object annotation is rejected, since the owner decides each
		// field's mutability.
		{
			name: "T3/MutInOwnedObjectAnnotation",
			src: `
				fn g() {
					val p: {a: mut {b: number}} = {a: {b: 0}}
				}
			`,
			want: []string{"3:21-3:22: owned-mutable field annotation is not allowed; the enclosing context decides mutability — wrap the whole annotation in `mut` to make this field writable, or use interior mutability"},
		},
		// A `readonly` field is written once, by the constructor call, so a later write is
		// rejected even through a `val mut` binding.
		{
			name: "T5/WriteReadonlyFieldAfterInit",
			src: `
				class C { readonly x: number }
				fn g() {
					val mut c = C(1)
					c.x = 2
				}
			`,
			want: []string{"5:6-5:9: cannot assign to 'x' because it is a readonly property"},
		},
		// A getter with no setter counts as a `readonly` field, so an assignment to it is rejected.
		{
			name: "T5/AssignGetterWithoutSetter",
			src: `
				class C {
					_x: number,
					get x(&self) -> number { return self._x },
				}
				fn g() {
					val mut c = C(1)
					c.x = 2
				}
			`,
			want: []string{"8:6-8:13: Property 'x' is read-only; it has a getter but no setter or field to write."},
		},
		// The constructor's first write initializes `x`, so its second write is rejected. D5.
		{
			name: "T5/WriteReadonlyFieldTwiceInConstructor",
			src: `
				class C {
					readonly x: number,
					constructor(&mut self, x: number) {
						self.x = x
						self.x = x + 1
					},
				}
			`,
			want: []string{"6:7-6:13: cannot assign to 'x' because it is a readonly property"},
		},
		// The example from T8. The branches of `a` mix the owned `p` with the borrow `q`, which is
		// rejected. The branches of `b` borrow `r` to match `q`, which is accepted. D3.
		{
			name: "T8/BranchesAgreeOnOwnership",
			src: `
				fn pick(cond: boolean, q: &{x: number}) -> number {
					val p = {x: 1}
					val r = {x: 2}
					val a = if cond { p } else { q }
					val b = if cond { &r } else { q }
					return b.x
				}
			`,
			want: []string{"5:13-5:38: a union or intersection mixes owned and borrowed members. Make ownership uniform first. Clone the borrowed member to own it, or borrow the owned member."},
		},
		// A setter writes its instance, so a setter declared with `&self` is rejected. D6.
		{
			name: "T10/SetterWithImmutableReceiver",
			src: `
				class C {
					_x: number,
					set x(&self, v: number) {},
				}
			`,
			want: []string{"4:6-4:32: Setter 'x' must declare a `&mut self` receiver; writing through it mutates the instance."},
		},
	})
}

// TestTypeRuleLiteralWidening covers the T9 examples for bindings. A `val` binding cannot be
// written, so it keeps the literal type of its initializer. A `var` binding can be reassigned to
// any value of the primitive, so it widens each literal.
func TestTypeRuleLiteralWidening(t *testing.T) {
	runLiteralWideningCases(t, []literalWideningCase{
		{
			name: "T9/ValNumber",
			src:  `val n = 5`,
			want: map[string]string{"n": "5"},
		},
		{
			name: "T9/VarNumber",
			src:  `var n = 5`,
			want: map[string]string{"n": "number"},
		},
		{
			name: "T9/ValObject",
			src:  `val p = {x: 1}`,
			want: map[string]string{"p": "{x: 1}"},
		},
		// The object is immutable, but the binding can be reassigned to any `{x: number}`.
		{
			name: "T9/VarObject",
			src:  `var p = {x: 1}`,
			want: map[string]string{"p": "{x: number}"},
		},
	})
}

// TestTypeRulesBindingMutability covers the T3 and T5 cases where a `val mut` binding with a type
// annotation owns a mutable value of the annotated type.
func TestTypeRulesBindingMutability(t *testing.T) {
	runTypeRuleCases(t, []typeRuleCase{
		// A `val mut` binding makes every owned layer of its annotated value writable.
		{
			name: "T3/DeepWriteThroughAnnotatedValMutBinding",
			src: `
				fn g() {
					val mut p: {a: {b: number}} = {a: {b: 0}}
					p.a.b = 1
				}
			`,
		},
		// The array is owned by a `val mut` binding, so `push` is accepted. Each element is a `&`
		// borrow, so the write through `b[0]` is rejected.
		{
			name: "T3/ValMutArrayOfImmutableBorrows",
			src: `
				type Point = {x: number, y: number}
				fn g(q: &Point) {
					val mut b: Array<&Point> = []
					b.push(q)
					b[0].x = 1
				}
			`,
			want: []string{"6:6-6:16: cannot constrain immutable Point <: mutable object"},
		},
		// A holder of the `val mut` binding could reassign `a`, so a `readonly` field cannot fill
		// it.
		{
			name: "T5/ReadonlyFieldIntoWritableSlot",
			src: `
				fn g() {
					val q: {readonly a: number} = {a: 1}
					val mut p: {a: number} = q
				}
			`,
			want: []string{"4:31-4:32: readonly field 'a' cannot satisfy a writable field requirement"},
		},
	})
}

// DISABLED until #2254, which rejects `mut` inside an owned type annotation and removes the
// owned-mutable type. The comment above each case says what the solver does today.
/*
func TestTypeRulesOwnedMutType(t *testing.T) {
	// Since `mut` is not part of an owned type, `Array<mut Point>` is rejected. Today the
	// annotation is accepted with no diagnostic.
	runTypeRuleCases(t, []typeRuleCase{
		{
			name: "T3/MutInOwnedTypeArgument",
			src: `
				type Point = {x: number, y: number}
				fn g() {
					val a: Array<mut Point> = []
				}
			`,
			want: []string{"4:19-4:28: `mut` is not allowed in an owned type; the binding decides mutability, so write `val mut` on the binding or borrow with `&mut`"},
		},
	})
	// T3 makes every owned layer of a `val mut` binding writable, so every literal widens. The
	// binding's mutability shows on its name as `mut p`, so the type carries no `mut`. Today the
	// type renders as `mut {x: number, y: {z: string}}`.
	runLiteralWideningCases(t, []literalWideningCase{
		{
			name: "T9/ValMutObject",
			src:  `val mut p = {x: 1, y: {z: "a"}}`,
			want: map[string]string{"p": "{x: number, y: {z: string}}"},
		},
	})
}
*/

// DISABLED until #2288, which infers a class field's type from its initializer and widens each
// writable field.
//
// Today an instance field needs a type annotation and cannot take an initializer, so the class
// below reports parse errors. A static field takes an initializer but keeps its literal type, so
// `count` infers as `0`.
/*
func TestTypeRuleClassFieldWidening(t *testing.T) {
	// The `Request` example from T9. A class field is writable unless it is `readonly`, so only
	// `retries` and `count` widen.
	runLiteralWideningCases(t, []literalWideningCase{
		{
			name: "T9/RequestFields",
			src: `
				class Request {
					readonly method = "GET",
					retries = 0,
					readonly static version = 2,
					static count = 0,
				}
				val r = Request()
				val method = r.method
				val retries = r.retries
				val version = Request.version
				val count = Request.count
			`,
			want: map[string]string{
				"Request": "{new () -> Request, readonly version: 2, count: number}",
				"r":       "Request",
				"method":  `"GET"`,
				"retries": "number",
				"version": "2",
				"count":   "number",
			},
		},
	})
}
*/

// DISABLED until #2287, which rejects a getter declared with `&mut self`.
//
// Today `GetterReceiverError` accepts a getter that takes `&self` or `&mut self`, so the class
// below checks clean. D6 decides that a getter takes `&self` only.
/*
func TestTypeRuleGetterReceiver(t *testing.T) {
	runTypeRuleCases(t, []typeRuleCase{
		{
			name: "T10/GetterWithMutableReceiver",
			src: `
				class C {
					_x: number,
					get x(&mut self) -> number { return self._x },
				}
			`,
			want: []string{"4:6-4:51: Getter 'x' must declare a `&self` receiver; reading through it leaves the instance in place."},
		},
	})
}
*/
