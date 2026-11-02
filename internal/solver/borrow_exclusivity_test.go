package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBorrowExclusivity covers the mutable-XOR-immutable rule: data one borrow can write through
// may not be reachable at the same time through a borrow that expects it to hold still. Two
// borrows that agree, both mutable or both immutable, are allowed.
//
// Each case asserts the full diagnostic, so the wording that says WHICH pair conflicts is part of
// what is asserted. A case that should be accepted asserts no diagnostic at all.
func TestBorrowExclusivity(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// The canonical case. One argument reads x and the other writes it, so the callee holds
		// two views of one value and one of them can change it.
		"ImmutableAndMutableOfOnePlace": {
			src: `
				declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					readWrite(&x, &mut x)
				}
			`,
			want: []string{"6:20-6:26: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		// Two writable views of one value both expect it to change, so neither is surprised by
		// the other's write. Rule 3 of planning/lifetimes/requirements.md allows this, where Rust
		// would not: one thread cannot observe a tear, and `&mut` is invariant in its referent, so
		// the two views agree on the type as well as on the mutability.
		"TwoMutableOfOnePlaceOk": {
			src: `
				declare fn writeWrite(a: &mut {v: number}, b: &mut {v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					writeWrite(&mut x, &mut x)
				}
			`,
			want: nil,
		},
		// Two readers see the same value, so nothing can disagree.
		"TwoImmutableOfOnePlaceOk": {
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					readRead(&x, &x)
				}
			`,
			want: nil,
		},
		// The parameter decides, not the argument. One argument is written `&mut` and the other
		// `&`, which would be the conflicting pair if the argument decided. The callee takes both
		// as immutable, so neither view can change the value.
		"MutableArgumentIntoAnImmutableParamOk": {
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					readRead(&mut x, &x)
				}
			`,
			want: nil,
		},
		// Field paths keep disjoint data apart. x.p and x.q name different values, so writing
		// one cannot be observed through a read of the other.
		"DisjointFieldsOk": {
			src: `
				declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined

				fn g() {
					val mut x = {p: {v: 1}, q: {v: 2}}
					readWrite(&x.p, &mut x.q)
				}
			`,
			want: nil,
		},
		// The same field through both arguments is the same data.
		"SameFieldConflicts": {
			src: `
				declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined

				fn g() {
					val mut x = {p: {v: 1}, q: {v: 2}}
					readWrite(&x.p, &mut x.p)
				}
			`,
			want: []string{"6:22-6:30: cannot borrow 'x.p' as mutable while it is borrowed as immutable"},
		},
		// A whole binding contains its fields, so borrowing x.p and then x reaches overlapping
		// data even though the paths differ in length. The message names both places, since
		// only part of x is the part already borrowed and the reader cannot tell which from
		// the blamed borrow alone.
		"WholeBindingOverlapsItsField": {
			src: `
				declare fn readWhole(a: &{v: number}, b: &mut {p: {v: number}}) -> undefined
				fn g() {
					val mut x = {p: {v: 1}}
					readWhole(&x.p, &mut x)
				}
			`,
			want: []string{"5:22-5:28: cannot borrow 'x' as mutable while 'x.p' is borrowed as immutable"},
		},
		// Separate bindings share no data.
		"DisjointRootsOk": {
			src: `
				declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					val mut y = {v: 2}
					readWrite(&x, &mut y)
				}
			`,
			want: nil,
		},
		// One borrow filling two parameters is the shape #794 names. m is a single borrow, and
		// what makes the pair a conflict is that the callee may write through one view while
		// reading the other.
		"OneBorrowFillingAnImmutableAndAMutableParam": {
			src: `
				declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined

				fn g(m: &mut {v: number}) {
					readWrite(m, m)
				}
			`,
			want: []string{"5:19-5:20: cannot borrow 'm' as mutable while it is borrowed as immutable"},
		},
		// The cross-statement form a call-site-only check misses. The conflict is reported at
		// the second borrow, which is where the program first holds two views of x.
		"BorrowsBoundToNamesConflict": {
			src: `
				declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					val a = &x
					val b = &mut x
					readWrite(a, b)
				}
			`,
			want: []string{"7:14-7:20: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		// A borrow nothing reads again constrains nothing, which is the NLL rule. Here a is
		// never read at all, so it and b are never live at once.
		"UnreadFirstBorrowOk": {
			src: `
				declare fn write(a: &mut {v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					val a = &x
					val b = &mut x
					write(b)
				}
			`,
			want: nil,
		},
		// A move conflicts with an immutable borrow too. After `val y = x` the data belongs to y,
		// and a still expects it to hold still.
		"MoveWhileImmutablyBorrowed": {
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					val a = &x
					val y = x
					readRead(a, a)
				}
			`,
			want: []string{"7:14-7:15: cannot move 'x' while it is borrowed"},
		},
		// Once nothing reads the borrow again, its loan is over and the move is the only path.
		"MoveAfterAnImmutableBorrowEndsOk": {
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					val a = &x
					readRead(a, a)
					val y = x
				}
			`,
			want: nil,
		},
		// A loan is live AT the statement that reads it, not only beyond it. b's last use is
		// the call, and the fresh `&x` in the same call is a second view of x while b can
		// still write. Asking only whether b outlives the call would miss this.
		// A borrow inside a literal takes its own loan rather than counting as a use of x. Two
		// mutable borrows of x are allowed.
		"MutableBorrowInALiteralBesideAMutableLoanOk": {
			src: `
				declare fn write(a: &mut {v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					val a = &mut x
					val t = {y: &mut x}
					write(a)
				}
			`,
			want: nil,
		},
		// An immutable borrow inside a literal beside a live mutable loan is the pair that
		// conflicts, and it reports as a borrow rather than as a use.
		"ImmutableBorrowInALiteralBesideAMutableLoan": {
			src: `
				declare fn write(a: &mut {v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					val a = &mut x
					val t = {y: &x}
					write(a)
				}
			`,
			want: []string{"7:18-7:20: cannot borrow 'x' as immutable while it is borrowed as mutable"},
		},
		// The owner is one more path to the value. A read through it beside a live borrow of
		// either kind sees nothing the borrow does not.
		"ReadThroughOwnerBesideAMutableLoanOk": {
			src: `
				declare fn write(a: &mut {v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					val a = &mut x
					val n = x.v
					write(a)
				}
			`,
			want: nil,
		},
		"ReadThroughOwnerBesideAnImmutableLoanOk": {
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					val a = &x
					val n = x.v
					readRead(a, a)
				}
			`,
			want: nil,
		},
		// A write through the owner beside a live mutable borrow is one more writer, which
		// Rule 3 allows.
		"WriteThroughOwnerBesideAMutableLoanOk": {
			src: `
				declare fn write(a: &mut {v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					val a = &mut x
					x.v = 5
					write(a)
				}
			`,
			want: nil,
		},
		// The same write beside a live immutable borrow changes data the borrow expects to
		// hold still.
		"WriteThroughOwnerBesideAnImmutableLoan": {
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					val a = &x
					x.v = 5
					readRead(a, a)
				}
			`,
			want: []string{"7:6-7:9: cannot assign to 'x.v' while it is borrowed as immutable"},
		},
		// A write reaches only the field it names, so a borrow of a disjoint field is unaffected
		// and a borrow of the field it writes into conflicts.
		"WriteBesideAnImmutableLoanOfADisjointFieldOk": {
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined

				fn g() {
					val mut x = {a: {v: 1}, b: {v: 2}}
					val r = &x.a
					x.b.v = 5
					readRead(r, r)
				}
			`,
			want: nil,
		},
		"NestedWriteBesideAnImmutableLoanOfItsField": {
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined

				fn g() {
					val mut x = {a: {v: 1}, b: {v: 2}}
					val r = &x.a
					x.a.v = 5
					readRead(r, r)
				}
			`,
			want: []string{"7:6-7:11: cannot assign to 'x.a.v' while it is borrowed as immutable"},
		},
		// Reassigning the whole binding leaves each borrow pointing at the old object, which
		// nothing writes through the owner anymore.
		//
		// DISABLED until #1762. A mutable borrow of a `var mut` binding reports a mutability
		// mismatch, so this case reports at `&mut x` and at `write(a)`. Once #1762 lands, it
		// checks.
		/*
			"ReassignBesideAMutableLoanOk": {
				src: `
					declare fn write(a: &mut {v: number}) -> undefined

					fn g() {
						var mut x = {v: 1}
						val a = &mut x
						x = {v: 2}
						write(a)
					}
				`,
				want: nil,
			},
		*/
		"ReassignBesideAnImmutableLoanOk": {
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined

				fn g() {
					var mut x = {v: 1}
					val a = &x
					x = {v: 2}
					readRead(a, a)
				}
			`,
			want: nil,
		},
		"FreshBorrowBesideALoanOnItsLastUse": {
			src: `
				declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					val b = &mut x
					readWrite(&x, b)
				}
			`,
			want: []string{"7:16-7:18: cannot borrow 'x' as immutable while it is borrowed as mutable"},
		},
		// A reassignment repoints the binding, so the loan it held is gone. a borrows y by the
		// time b is created, and nothing then reaches x twice.
		"ReassignedBorrowDropsItsOldLoanOk": {
			src: `
				declare fn write(a: &mut {v: number}) -> undefined

				fn g(mut x: {v: number}, mut y: {v: number}) {
					var a = &mut x
					a = &mut y
					val b = &mut x
					write(a)
				}
			`,
			want: nil,
		},
		// The reassignment does not silence a real conflict: a borrows x mutably after it, and
		// the immutable borrow of b then reads data a can write.
		"ReassignedBorrowStillConflicts": {
			src: `
				declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined

				fn g(mut x: {v: number}, mut y: {v: number}) {
					var a = &mut y
					a = &mut x
					val b = &x
					readWrite(b, a)
				}
			`,
			want: []string{"7:14-7:16: cannot borrow 'x' as immutable while it is borrowed as mutable"},
		},
		// A reassignment ends the binding's loan from that point on, and no further back. The
		// move of x sits before it, while a still borrows x, so it reports even though a is
		// repointed later in the body.
		"AReassignmentDoesNotSilenceAnEarlierMove": {
			src: `
				declare fn write(a: &mut {v: number}) -> undefined

				fn g(mut x: {v: number}, mut y: {v: number}) -> undefined {
					var a = &mut x
					val w = x
					write(a)
					a = &mut y
					write(a)
				}
			`,
			want: []string{"6:14-6:15: cannot move 'x' while it is borrowed"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

// TestExplicitBorrowArgs covers #1541: a borrow parameter takes a borrow, so the call writes
// one. What stays implicit is a method call's receiver, whose mode the signature already names.
func TestExplicitBorrowArgs(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// A bare place filling an immutable parameter reports, naming the borrow it needs.
		"BarePlaceIntoAnImmutableParam": {
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined
				declare fn write(a: &mut {v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					write(&mut x)
					readRead(x, &x)
				}
			`,
			want: []string{"8:15-8:16: this argument is borrowed by the callee, so write the borrow: `&`"},
		},
		// The mutable parameter asks for `&mut`, so the message names that instead.
		"BarePlaceIntoAMutableParam": {
			src: `
				declare fn write(a: &mut {v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					write(x)
				}
			`,
			want: []string{"6:12-6:13: this argument is borrowed by the callee, so write the borrow: `&mut`"},
		},
		// Writing the borrow is what the rule asks for.
		"WrittenBorrowsOk": {
			src: `
				declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined

				fn g() {
					val mut x = {v: 1}
					val mut y = {v: 2}
					readWrite(&x, &mut y)
				}
			`,
			want: nil,
		},
		// A value that is ALREADY a borrow is handed on rather than borrowed afresh, so there
		// is no borrow missing at this call and nothing to write.
		"PassingAnExistingBorrowOk": {
			src: `
				declare fn write(a: &mut {v: number}) -> undefined

				fn g(m: &mut {v: number}) {
					write(m)
				}
			`,
			want: nil,
		},
		// An owned parameter is moved into the callee rather than borrowed, so it needs no
		// borrow written and the rule leaves it alone.
		"OwnedParamTakesABarePlaceOk": {
			src: `
				declare fn consume(a: {v: number}) -> undefined
				fn g() {
					val x = {v: 1}
					consume(x)
				}
			`,
			want: nil,
		},
		// The receiver of a method call keeps auto-borrowing. `bump(&mut self)` names the mode
		// in the signature, and `c.bump()` has no second reading for a written borrow to
		// disambiguate.
		"MethodReceiverStillAutoBorrowsOk": {
			src: `
				class Counter {
					n: number,
					bump(&mut self) -> undefined { self.n = 1 },
				}
				fn g() {
					val mut c = Counter(0)
					c.bump()
				}
			`,
			want: nil,
		},
		// A method's own arguments are arguments like any other, so the receiver's exemption
		// does not extend to them.
		"MethodArgumentsAreNotExempt": {
			src: `
				class Holder {
					v: number,
					take(&self, a: &{v: number}) -> undefined {},
				}
				fn g() {
					val h = Holder(0)
					val x = {v: 1}
					h.take(x)
				}
			`,
			want: []string{"9:13-9:14: this argument is borrowed by the callee, so write the borrow: `&`"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

// TestExplicitBorrowFlowSites covers rule F4 at the flow sites other than a call argument. A
// value that fills a `&` or `&mut` destination has the borrow written where it flows, or the
// site reports the borrow it needs.
func TestExplicitBorrowFlowSites(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// B1 in planning/ownership_rules/requirements.md. The store into the `&` field reports
		// the missing `&` and does not move p, so the later read of p.x reports nothing. B1
		// writes `val mut h: {slot: …}`, which #2251 makes mutable. Until then the binding is
		// written with a `mut` annotation instead.
		"StoreIntoABorrowFieldB1": {
			src: `
				export fn go() -> number {
					val p = {x: 1}
					val q = {x: 2}
					val mut h: {slot: &{x: number}} = {slot: &q}
					h.slot = p
					return p.x + h.slot.x
				}
			`,
			want: []string{"6:15-6:16: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		// B3 in planning/ownership_rules/requirements.md. The annotated binding reports the
		// missing `&`. The next case writes it, and the move check then sees the borrow.
		"AnnotationOnlyBorrowB3": {
			src: `
				fn take(p: {x: number}) -> number {
					val mut q = p
					q.x = 5
					return q.x
				}
				export fn go() -> number {
					val p = {x: 1}
					val c: &{x: number} = p
					val n = take(p)
					return c.x + n
				}
			`,
			want: []string{"9:28-9:29: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		// With the borrow written, B3's program reports the move of p while c borrows it.
		"WrittenBorrowThenMoveB3": {
			src: `
				fn take(p: {x: number}) -> number {
					val mut q = p
					q.x = 5
					return q.x
				}
				export fn go() -> number {
					val p = {x: 1}
					val c: &{x: number} = &p
					val n = take(p)
					return c.x + n
				}
			`,
			want: []string{"10:19-10:20: cannot move 'p' while it is borrowed"},
		},
		// Each branch of a conditional produces the value, so each branch that yields an owned
		// value reports on its own.
		"EachBranchReports": {
			src: `
				fn g(cond: boolean) {
					val p = {x: 1}
					val mut q = {x: 2}
					val a: &{x: number} = if cond { p } else { q }
					val b: &mut {x: number} = do { q }
				}
			`,
			want: []string{
				"5:38-5:39: this value is borrowed by its destination, so write the borrow: `&`",
				"5:49-5:50: this value is borrowed by its destination, so write the borrow: `&`",
				"6:37-6:38: this value is borrowed by its destination, so write the borrow: `&mut`",
			},
		},
		// A borrow written on a call result borrows the value the call hands back. The `&` binds
		// looser than the call, so `&mk()` is `&(mk())`.
		"BorrowOfCallResult": {
			src: `
				fn mk() -> {x: number} { return {x: 1} }
				fn g() -> number {
					val s: &{x: number} = &mk()
					val t = &mk()
					return s.x + t.x
				}
			`,
			want: nil,
		},
		// DISABLED until #2324, which decides whether a `&mut` borrow of a call result is allowed.
		// The call hands back the only reference to its value, so the borrow aliases nothing.
		// Today it is rejected with `cannot constrain immutable object <: mutable object`.
		/*
			"MutBorrowOfCallResult": {
				src: `
					fn mk() -> {x: number} { return {x: 1} }
					fn bump(p: &mut {x: number}) { p.x = 2 }
					fn g() {
						val s: &mut {x: number} = &mut mk()
						s.x = 2
						bump(&mut mk())
					}
				`,
				want: nil,
			},
		*/
		// A source reported for an unwritten `&mut` is checked against the destination as if it
		// were `&`, so the missing borrow is the one fault reported for its mutability. A wrong
		// shape is still reported.
		"MissingMutBorrowWrongShape": {
			src: `
				fn g() {
					val mut q = {y: "a"}
					val b: &mut {x: number} = do { q }
				}
			`,
			want: []string{
				"4:37-4:38: this value is borrowed by its destination, so write the borrow: `&mut`",
				"4:22-4:28: object is missing property: x",
				"4:32-4:40: object has extra property: y",
			},
		},
		"MissingMutBorrowInReassignment": {
			src: `
				fn g(cond: boolean) {
					val mut q = {x: 2}
					var b: &mut {x: number} = &mut q
					b = if cond { q } else { q }
				}
			`,
			want: []string{
				"5:20-5:21: this value is borrowed by its destination, so write the borrow: `&mut`",
				"5:31-5:32: this value is borrowed by its destination, so write the borrow: `&mut`",
			},
		},
		"MissingMutBorrowInFieldStore": {
			src: `
				fn g(mut h: {slot: &mut {x: number}}) {
					val mut q = {x: 2}
					h.slot = do { q }
				}
			`,
			want: []string{"4:20-4:21: this value is borrowed by its destination, so write the borrow: `&mut`"},
		},
		// A literal element is checked against the matching slot of the literal's destination,
		// including when the literal is a call argument or nested in another literal.
		"LiteralElements": {
			src: `
				declare fn hold(h: {slot: &{x: number}}) -> undefined
				fn g() -> number {
					val p = {x: 1}
					val mut m = {x: 2}
					hold({slot: p})
					val t: [number, &{x: number}] = [1, p]
					val a: Array<&{x: number}> = [p]
					val o: {inner: {slot: &mut {x: number}}} = {inner: {slot: m}}
					return p.x + m.x
				}
			`,
			want: []string{
				"6:18-6:19: this value is borrowed by its destination, so write the borrow: `&`",
				"7:42-7:43: this value is borrowed by its destination, so write the borrow: `&`",
				"8:36-8:37: this value is borrowed by its destination, so write the borrow: `&`",
				"9:64-9:65: this value is borrowed by its destination, so write the borrow: `&mut`",
			},
		},
		// An unwritten borrow of a module-level binding reports the missing `&` alone, not also
		// a move out of module-level state.
		"ModuleLevelSourceReportsOnce": {
			src: `
				val xs: Array<number> = [1]
				fn g() -> &Array<number> {
					val a: &Array<number> = xs
					return xs
				}
			`,
			want: []string{
				"4:30-4:32: this value is borrowed by its destination, so write the borrow: `&`",
				"5:13-5:15: this value is borrowed by its destination, so write the borrow: `&`",
			},
		},
		// An owned union fills a borrow like any owned value. A borrow destination may also allow
		// `undefined`, and it may be named through an alias.
		"UnionsOptionalsAndAliases": {
			src: `
				type U = {a: number} | {b: number}
				type R = &{f: &{x: number}}
				type B = &{x: number}
				fn g(u: U) {
					val p = {x: 1}
					val a: &U = u
					val b: &{x: number} | undefined = p
					val r: R = &{f: p}
					val s: B | undefined = p
				}
			`,
			want: []string{
				"7:18-7:19: this value is borrowed by its destination, so write the borrow: `&`",
				"8:40-8:41: this value is borrowed by its destination, so write the borrow: `&`",
				"9:22-9:23: this value is borrowed by its destination, so write the borrow: `&`",
				"10:29-10:30: this value is borrowed by its destination, so write the borrow: `&`",
			},
		},
		// A borrow that is already a value is copied, so nothing is missing. A destination
		// written through an alias of a borrow type is a borrow like any other.
		"ExistingBorrowsAndAliasesOk": {
			src: `
				type R = &{x: number}
				fn g(r: &{x: number}, s: R) -> R {
					val a: R = r
					val b: &{x: number} = s
					val p = {x: 1}
					val c: R = &p
					return s
				}
			`,
			want: nil,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

// TestFieldReadThroughBorrow covers rule F4 for a field read through a borrow. A read that
// yields a borrow of an object field has the borrow written, or the site reports the borrow it
// needs. The cases that need nothing written check clean.
func TestFieldReadThroughBorrow(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// The example under F4 in planning/ownership_rules/requirements.md.
		"RuleExample": {
			src: `
				class Point { x: number, y: number }
				class Line { start: Point, width: number }
				fn first(line: &Line) -> &Point {
					val w = line.width
					val x = line.start.x
					val {start} = line
					val s = line.start
					return &line.start
				}
			`,
			want: []string{"8:14-8:24: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		// An unannotated destination takes the read's own mutability, so a read through
		// `&mut` asks for `&mut`.
		"MutableReceiverAsksForMut": {
			src: `
				fn g(h: &mut {f: {x: number}}) {
					val d = h.f
				}
			`,
			want: []string{"3:14-3:17: this value is borrowed by its destination, so write the borrow: `&mut`"},
		},
		// A borrow destination decides the mutability, so a `&` destination asks for `&` even
		// through a `&mut` receiver.
		"BorrowDestinationDecidesMutability": {
			src: `
				fn g(h: &mut {f: {x: number}}) -> &{x: number} {
					return h.f
				}
			`,
			want: []string{"3:13-3:16: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		// A chain reports its last link, and an unannotated return and an element of an
		// unannotated literal are destinations like any other.
		"ChainsReturnsAndLiterals": {
			src: `
				fn g(h: &{f: {g: {x: number}}}) {
					val o = [h.f.g, {k: h.f}]
					return h.f.g
				}
			`,
			want: []string{
				"3:15-3:20: this value is borrowed by its destination, so write the borrow: `&`",
				"3:26-3:29: this value is borrowed by its destination, so write the borrow: `&`",
				"4:13-4:18: this value is borrowed by its destination, so write the borrow: `&`",
			},
		},
		// An optional field reads as its borrow or `undefined`, and still needs the borrow
		// written.
		"OptionalField": {
			src: `
				fn g(h: &{f?: {x: number}}) {
					val d = h.f
				}
			`,
			want: []string{"3:14-3:17: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		// An optional chain still borrows, but `&h?.f` is not supported, so the report tells
		// the program to bind the receiver first instead of suggesting `&`.
		"OptionalChain": {
			src: `
				fn g(h: &{f: {x: number}} | undefined) {
					val d = h?.f
				}
			`,
			want: []string{
				"3:14-3:18: this optional chain is borrowed by its destination, and an optional chain cannot be borrowed, so bind the receiver with `if val` and borrow the field through it",
			},
		},
		// Binding the receiver with `if val` lets the plain read be borrowed.
		"BoundOptionalReceiverOk": {
			src: `
				fn g(h: &{f: {x: number}} | undefined) {
					if val k = h {
						val d = &k.f
					}
				}
			`,
		},
		// An owned destination rejects the read by T6 alone, since a written `&` would not
		// fill it either.
		"OwnedDestinationReportsT6": {
			src: `
				fn g(h: &{f: {x: number}}) {
					val d: {x: number} = h.f
				}
			`,
			want: []string{"3:13-3:24: cannot use borrowed &object as owned object"},
		},
		// None of these reads creates a borrow, so none needs one written:
		//   - `o.f` reads an owned receiver and yields the owned field.
		//   - `r.f` hands on the borrow the field already holds.
		//   - `h.f.x` and `h.n` end at a value type and copy.
		//   - `h.m` reads a function, which copies.
		//   - `h.f.get()` borrows `h.f` as a method receiver.
		//   - `{f} = h` is a pattern over a borrow.
		"NothingToWrite": {
			src: `
				class C { v: number, get(&self) -> number { return self.v } }
				fn g(
					o: {f: {x: number}},
					r: &{f: &{x: number}},
					h: &{f: {x: number}, n: number, m: fn () -> number},
					c: &{f: C},
				) -> number {
					val a = o.f
					val b = r.f
					val x = h.f.x
					val n = h.n
					val m = h.m
					val {f} = h
					return c.f.get() + x + n + m() + f.x
				}
			`,
			want: nil,
		},
		// A field typed through an alias reads as the type the alias names, so an alias of a
		// value type, a borrow, or a function needs nothing written.
		"AliasedFieldsNothingToWrite": {
			src: `
				type Id = string
				type PR = &{x: number}
				type F = fn () -> number
				fn g(h: &{id: Id, p: PR, f: F}) -> number {
					val i = h.id
					val p = h.p
					val f = h.f
					return p.x
				}
			`,
			want: nil,
		},
		// An alias of an object type lends a borrow like the object type it names.
		"AliasedObjectField": {
			src: `
				type P = {x: number}
				fn g(h: &{p: P}) {
					val p = h.p
				}
			`,
			want: []string{"4:14-4:17: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		// A parameter whose type is an alias of a borrow reads its fields through that borrow.
		"AliasedBorrowReceiver": {
			src: `
				type HP = &{p: {x: number}}
				fn g(h: HP) {
					val p = h.p
				}
			`,
			want: []string{"4:14-4:17: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		// A generic parameter takes its type from the argument, like an unannotated binding. The
		// fix borrows the argument, `id(&h.f)`. The prefix `&` binds looser than a call, so
		// `&id(h.f)` borrows the result and leaves the argument unwritten.
		"GenericParameter": {
			src: `
				fn id<T>(x: T) -> T { return x }
				fn g(h: &{f: {x: number}}) {
					val s = id(h.f)
				}
			`,
			want: []string{"4:17-4:20: this argument is borrowed by the callee, so write the borrow: `&`"},
		},
		"GenericParameterBorrowWritten": {
			src: `
				fn id<T>(x: T) -> T { return x }
				fn g(h: &{f: {x: number}}) {
					val s = id(&h.f)
				}
			`,
			want: nil,
		},
		// Borrowing the call's result leaves the argument unwritten, so the error stays on it.
		"GenericParameterResultBorrowed": {
			src: `
				fn id<T>(x: T) -> T { return x }
				fn g(h: &{f: {x: number}}) {
					val s = &id(h.f)
				}
			`,
			want: []string{"4:18-4:21: this argument is borrowed by the callee, so write the borrow: `&`"},
		},
		// A field typed `&mut` keeps its own mutability through a `&` receiver, as T3 states, and
		// a read of a borrow-typed field copies the borrow, so nothing needs writing.
		"MutBorrowFieldThroughImmutableReceiver": {
			src: `
				class Box { v: number }
				class C { readonly inner: &mut Box }
				fn g(c: &C) {
					val b = c.inner
				}
			`,
			want: nil,
		},
		// A return annotation that does not resolve names no destination, so only the
		// annotation is reported.
		"UnresolvedReturnAnnotation": {
			src: `
				fn g(h: &{f: {x: number}}) -> Unknown {
					return h.f
				}
			`,
			want: []string{"2:35-2:42: cannot find type `Unknown`"},
		},
		// A written borrow of an optional field keeps the `undefined` the field may hold.
		"WrittenOptionalFieldKeepsUndefined": {
			src: `
				fn g(h: &{f?: {x: number}}) -> number {
					val d: &{x: number} = &h.f
					return d.x
				}
			`,
			want: []string{"3:28-3:32: cannot constrain undefined <: object"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

// TestStoreEffectLoans covers the borrow a call's store effect creates. A signature that writes
// one argument into another leaves the target reaching the item, so the target holds a borrow
// of it that an immutable borrow or a move of the item has to respect.
//
// Each case keeps the target live past the read, since a loan lasts only as long as the binding
// holding it. The last case drops that use to show the rule turning off.
func TestStoreEffectLoans(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// An immutable borrow of the stored item conflicts with the mutable loan the store
		// leaves, since the target can still write through it.
		"SecondBorrowAfterAStore": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				declare fn readImmBorrow(x: &{value: number}) -> undefined
				declare fn touch<'d>(x: &'d mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined

				fn f(mut q: {value: number}, mut r: {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					readImmBorrow(&b)
					touch(&mut a)
				}
			`,
			want: []string{"13:20-13:22: cannot borrow 'b' as immutable while it is borrowed as mutable"},
		},
		// Moving the item out while the target still reaches it leaves the target pointing at
		// data the new owner controls. The loan-against-loan check does not see this, because a
		// move is not a borrow.
		"MoveAfterAStore": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				declare fn touch<'d>(x: &'d mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined

				fn g(mut q: {value: number}, mut r: {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					val y = b
					touch(&mut a)
				}
			`,
			want: []string{"12:14-12:15: cannot move 'b' while it is borrowed"},
		},
		// Repointing the field the store wrote to ends the loan there, so the item is reachable
		// one way again. This is the loan side of the strong update the borrow graph makes on
		// the same subtree.
		"RepointingTheStoredFieldReleasesTheItemOk": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				declare fn touch<'d>(x: &'d mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined

				fn f(mut q: {value: number}, mut r: {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut c = {value: 3}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					a.peer = &mut c
					val y = b
					touch(&mut a)
				}
			`,
			want: nil,
		},
		// A store into a SIBLING field leaves the loan at [peer] holding, since the update
		// reaches only the field it writes.
		"RepointingASiblingFieldKeepsTheLoan": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				declare fn touch<'d>(x: &'d mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined

				fn g(mut q: {value: number}, mut r: {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut c = {value: 3}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					a.spare = &mut c
					val y = b
					touch(&mut a)
				}
			`,
			want: []string{"14:14-14:15: cannot move 'b' while it is borrowed"},
		},
		// A move walked BEFORE the repoint happened while the loan still held, so it keeps its
		// diagnostic. The loan carries the sequence it ended at rather than leaving the list,
		// which is what keeps a later store from silencing an earlier move.
		"AMoveBeforeTheRepointKeepsItsDiagnostic": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				declare fn touch<'d>(x: &'d mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined

				fn h(mut q: {value: number}, mut r: {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut c = {value: 3}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					val y = b
					a.peer = &mut c
					touch(&mut a)
				}
			`,
			want: []string{"13:14-13:15: cannot move 'b' while it is borrowed"},
		},
		// Nothing reads the target after the store, so its borrow of the item is dead and the
		// item is reachable one way again. This is the same NLL rule a named borrow follows.
		// A borrow in a returned literal takes its own loan, so the `&mut b` here is a second
		// mutable borrow of b beside the store's loan, which is allowed.
		"MutableBorrowInAReturnAfterAStoreOk": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined

				fn g(mut q: {value: number}, mut r: {value: number}) -> [&mut {value: number}, &mut {value: number}] {
					val mut b = {value: 2}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					return [a.spare, &mut b]
				}
			`,
			want: nil,
		},
		// The same holds for a borrow inside a literal a binding holds.
		"MutableBorrowInALiteralAfterAStoreOk": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				declare fn touch<'d>(x: &'d mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined

				fn g(mut q: {value: number}, mut r: {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					val t = {x: &mut b}
					touch(&mut a)
				}
			`,
			want: nil,
		},
		// A read of the stored item through its owner beside the store's mutable loan sees
		// nothing the loan does not.
		"ReadOfTheItemAfterAStoreOk": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				declare fn touch<'d>(x: &'d mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined

				declare fn useNum(n: number) -> undefined
				fn g(mut q: {value: number}, mut r: {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					useNum(b.value)
					touch(&mut a)
				}
			`,
			want: nil,
		},
		"FieldReadOfTheItemIntoABindingAfterAStoreOk": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				declare fn touch<'d>(x: &'d mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined

				fn g(mut q: {value: number}, mut r: {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					val n = b.value
					touch(&mut a)
				}
			`,
			want: nil,
		},
		"DeadTargetReleasesTheItemOk": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined

				fn h(mut q: {value: number}, mut r: {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					val y = b
				}
			`,
			want: nil,
		},
		// Without the store the target reaches nothing of b's, so both the borrow and the read
		// are the only path to it.
		"NoStoreLeavesTheItemFreeOk": {
			src: `
				declare fn readMutBorrow(x: &mut {value: number}) -> undefined
				declare fn touch<'d>(x: &'d mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined

				fn k(mut q: {value: number}, mut r: {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &mut q, spare: &mut r}
					readMutBorrow(&mut b)
					val y = b
					touch(&mut a)
				}
			`,
			want: nil,
		},
		// The store's own arguments are compared against each other by the loan-against-loan
		// check, not against the loan that same call creates. Reading b to pass `&mut b` is how
		// the store is written, so it reports nothing on its own.
		"TheStoreCallItselfIsQuietOk": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				declare fn touch<'d>(x: &'d mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined

				fn m(mut q: {value: number}, mut r: {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					touch(&mut a)
				}
			`,
			want: nil,
		},
		// What the store puts in the target decides whether a write can go through it. A
		// signature storing an immutable `&'a B` leaves the target able to read the item and not
		// to write it, so a second immutable borrow is two readers of one value.
		"ImmutableItemStoredLeavesItReadableOk": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a {value: number}, spare: &'b mut {value: number}},
					item: &'a {value: number},
				) -> undefined
				declare fn readImmBorrow(x: &{value: number}) -> undefined
				declare fn hold<'d>(x: &'d mut {peer: &{value: number}, spare: &mut {value: number}}) -> undefined
				fn f(q: {value: number}, mut r: {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &q, spare: &mut r}
					store(&mut a, &b)
					readImmBorrow(&b)
					hold(&mut a)
				}
			`,
			want: nil,
		},
		// A direct store puts the argument's own place in the target, field path and all, so
		// the target reaches b.inner and nothing else of b. Borrowing the disjoint b.other
		// reaches data the target cannot write.
		"StoreOfOneFieldLeavesItsSiblingFreeOk": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				declare fn readMutBorrow(x: &mut {value: number}) -> undefined
				declare fn touch<'d>(x: &'d mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined

				fn f(mut q: {value: number}, mut r: {value: number}) -> undefined {
					val mut b = {inner: {value: 1}, other: {value: 2}}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b.inner)
					readMutBorrow(&mut b.other)
					touch(&mut a)
				}
			`,
			want: nil,
		},
		// A read is weighed against the borrows that existed when it was walked. The borrow on
		// the else arm comes later in the source, so it never reaches the read on the then arm.
		"ABorrowOnOneArmDoesNotReachTheOtherOk": {
			src: `
				declare fn readMutBorrow(x: &mut {value: number}) -> undefined
				fn f(cond: boolean, mut x: {value: number}) -> undefined {
					var a = &mut x
					if cond {
						val y = x
					} else {
						a = &mut x
						readMutBorrow(a)
					}
				}
			`,
			want: nil,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

// TestMutSelfIsAMutableStoreSource covers the loan a method's own receiver creates when the
// method drains it into a parameter.
//
// A `mut self` receiver is a mutable RefType carrying no lifetime, the same shape an
// owned-mutable parameter takes, so the lifetime test that decides an ordinary parameter's
// mutability does not describe it. Reading it as immutable would let the target hold a writable
// view while the check believed it held a read-only one.
//
// The later IMMUTABLE borrow of h is what exposes this. It conflicts with a mutable loan and not
// with an immutable one, so the diagnostic appears only when the receiver was read as mutable.
func TestMutSelfIsAMutableStoreSource(t *testing.T) {
	_, _, errs := inferSource(t, `
		class Holder<'a> {
			peer: &'a mut {value: number},
			drain(&mut self, out: &mut {slot: &'a mut {value: number}}) -> undefined { out.slot = self.peer },
		}
		declare fn touch<'e>(x: &'e mut {slot: &mut {value: number}}) -> undefined
		fn build(mut p: {value: number}) -> undefined {
			val mut h = Holder(&mut p)
			val mut o = {slot: &mut p}
			h.drain(&mut o)
			val again = &h
			touch(&mut o)
		}
	`)
	require.Equal(t, []string{
		"11:16-11:18: cannot borrow 'h' as immutable while it is borrowed as mutable",
	}, messagesWithSpan(t, errs))
}
