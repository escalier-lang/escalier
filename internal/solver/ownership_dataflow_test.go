package solver

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// inferWithDataflow infers src against the standard library with the dataflow pass on and
// returns the message of every diagnostic the program reports.
func inferWithDataflow(t *testing.T, src string) []string {
	t.Helper()

	res := InferModuleAgainstStdlib(parseModule(t, src), benchTree, Options{OwnershipDataflow: true})
	msgs := make([]string, len(res.Errors))
	for i, e := range res.Errors {
		msgs[i] = e.Message()
	}
	return msgs
}

// TestOwnershipDataflow covers E1, E2, and E3 with the dataflow pass on: a program each rule
// rejects, a program each rule allows, moves and borrows across joins and loops, borrows that
// last for a statement or for the rest of the program, and a diagnostic the pass leaves to the
// other checks. errs lists every diagnostic the program reports, and is nil for a program that checks
// clean.
func TestOwnershipDataflow(t *testing.T) {
	cases := []struct {
		name string
		src  string
		errs []string
	}{
		{
			name: "E1UseAfterMove",
			src: `
				fn go() -> number {
					val p = {x: 1}
					val q = p
					return p.x + q.x
				}
			`,
			errs: []string{"use of moved value 'p'"},
		},
		{
			name: "E1PartialMoveLeavesSiblingUsable",
			src: `
				fn go() -> number {
					val pair = {a: {x: 1}, b: {x: 2}}
					val a = pair.a
					return a.x + pair.b.x
				}
			`,
		},
		{
			name: "E1PartiallyMovedWhole",
			src: `
				fn go() -> number {
					val pair = {a: {x: 1}, b: {x: 2}}
					val a = pair.a
					val whole = pair
					return a.x + whole.b.x
				}
			`,
			errs: []string{"use of partially moved value 'pair'; field 'pair.a' was moved out"},
		},
		{
			name: "E1ReassignmentReinitializes",
			src: `
				fn go() -> number {
					var p = {x: 1}
					val q = p
					p = {x: 2}
					return p.x + q.x
				}
			`,
		},
		{
			name: "E1MoveOnOneBranchOfJoin",
			src: `
				fn go(cond: boolean) -> number {
					val p = {x: 1}
					if cond {
						val q = p
					}
					return p.x
				}
			`,
			errs: []string{"use of moved value 'p'"},
		},
		{
			name: "E1MoveOnBothBranchesUsedInNeither",
			src: `
				fn go(cond: boolean) -> number {
					val p = {x: 1}
					if cond {
						val q = p
						return q.x
					} else {
						val r = p
						return r.x
					}
				}
			`,
		},
		{
			// Only one arm runs, so the move in one arm leaves p usable in the other.
			name: "E1MoveInEachArmOfReturnedIf",
			src: `
				fn go(c: boolean, p: {a: {x: number}}) {
					return if c { p } else { p }
				}
			`,
		},
		{
			name: "E1PartialMoveInOneArmOfReturnedIf",
			src: `
				fn go(c: boolean, p: {a: {x: number}}) {
					return if c { p.a } else { p }
				}
			`,
		},
		{
			name: "E1MoveInEachArmOfReturnedMatch",
			src: `
				fn go(c: boolean, p: {a: {x: number}}) {
					return match c {
						true => p,
						false => p,
					}
				}
			`,
		},
		{
			name: "E1MoveInEachArmOfIfBoundThenReturned",
			src: `
				fn go(c: boolean, p: {a: {x: number}}) {
					val r = if c { p } else { p }
					return r
				}
			`,
		},
		{
			name: "E1ReturnInEachArmOfIf",
			src: `
				fn go(c: boolean, p: {a: {x: number}}) {
					if c {
						return p
					} else {
						return p
					}
				}
			`,
		},
		{
			// The first arm moves p twice on one path, so E1 still rejects it.
			name: "E1DoubleMoveInOneArmOfReturnedIf",
			src: `
				fn go(c: boolean, p: {a: {x: number}}) {
					return if c { [p, p] } else { p }
				}
			`,
			errs: []string{"use of moved value 'p'"},
		},
		{
			name: "E1MoveInLoop",
			src: `
				fn go(items: Array<number>) {
					val p = {x: 1}
					for item in items {
						val q = p
					}
				}
			`,
			errs: []string{"use of moved value 'p'"},
		},
		{
			// The loop may run zero times, so p can still be moved when the return reads it.
			name: "E1ReassignedOnlyInLoop",
			src: `
				fn go(items: Array<number>) -> number {
					var p = {x: 1}
					val q = p
					for item in items {
						p = {x: item}
					}
					return p.x + q.x
				}
			`,
			errs: []string{"use of moved value 'p'"},
		},
		{
			// Writing p.a.x reads p to reach the field, so it uses the moved p.
			name: "E1WriteUnderMovedPlace",
			src: `
				fn go() -> number {
					var mut p = {a: {x: 1}}
					val q = p
					p.a.x = 2
					return q.a.x
				}
			`,
			errs: []string{"use of moved value 'p'"},
		},
		{
			// noop is never called. It is here because its body records no ownership events,
			// so tableSpan gives it no span when the pass orders the bodies. The pass must still
			// check go after it.
			name: "E1BesideBodyWithoutEvents",
			src: `
				fn noop() {}

				fn go() -> number {
					val p = {x: 1}
					val q = p
					return p.x + q.x
				}
			`,
			errs: []string{"use of moved value 'p'"},
		},
		{
			name: "E2WriteWhileBorrowed",
			src: `
				fn go() -> number {
					val mut p = {x: 0}
					val q: &{x: number} = &p
					p.x = 1
					return q.x
				}
			`,
			errs: []string{"cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			name: "E2WriteThroughCopiedBorrow",
			src: `
				fn go() -> number {
					val mut p = {x: 0}
					val c = &p
					val d = c
					p.x = 1
					return d.x
				}
			`,
			errs: []string{"cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			name: "E2WriteAfterLastUse",
			src: `
				fn go() -> number {
					val mut p = {x: 0}
					p.x = 1
					val q: &{x: number} = &p
					return q.x
				}
			`,
		},
		{
			name: "E2ReassignBesideLiveBorrow",
			src: `
				fn go() -> number {
					var mut p = {x: 0}
					val q: &{x: number} = &p
					p = {x: 1}
					return q.x + p.x
				}
			`,
		},
		{
			name: "E2DynamicIndexWriteWhileBorrowed",
			src: `
				fn go(i: number) -> number {
					val mut xs: Array<{x: number}> = [{x: 0}]
					val q: &Array<{x: number}> = &xs
					xs[i] = {x: 1}
					return q.length
				}
			`,
			errs: []string{"cannot assign to 'xs[i]' while it is borrowed as immutable"},
		},
		{
			name: "E2WriteWhileMovedHolderLive",
			src: `
				fn go() -> number {
					val mut p = {x: 0}
					val o = {f: &p}
					val o2 = o
					p.x = 1
					return o2.f.x
				}
			`,
			errs: []string{"cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			name: "E2WriteAfterHolderReassigned",
			src: `
				fn go() -> number {
					val mut p = {x: 0}
					val q = {x: 0}
					var c: &{x: number} = &p
					c = &q
					p.x = 1
					return c.x
				}
			`,
		},
		{
			name: "E2WriteWhileBranchAssignedHolderLive",
			src: `
				fn go(cond: boolean) -> number {
					val mut a = {x: 0}
					val b = {x: 0}
					val r = {x: 0}
					var c: &{x: number} = &r
					c = if cond { &a } else { &b }
					a.x = 1
					return c.x
				}
			`,
			errs: []string{"cannot assign to 'a.x' while it is borrowed as immutable"},
		},
		{
			name: "E2DynamicIndexWriteKeepsOtherElements",
			src: `
				fn go(i: number) -> number {
					val mut p = {x: 0}
					val q = {x: 0}
					var mut xs: Array<&{x: number}> = [&p]
					xs[i] = &q
					p.x = 1
					return xs[0].x
				}
			`,
			errs: []string{"cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			name: "E2MutableBorrowWhileImmutableLive",
			src: `
				fn go() -> number {
					val mut p = {x: 0}
					val a = &p
					val b = &mut p
					b.x = 1
					return a.x
				}
			`,
			errs: []string{"cannot borrow 'p' as mutable while it is borrowed as immutable"},
		},
		{
			name: "E2TwoMutableBorrows",
			src: `
				fn go() -> number {
					val mut p = {x: 0}
					val a = &mut p
					val b = &mut p
					b.x = 1
					a.x = 2
					return p.x
				}
			`,
		},
		{
			name: "E2BorrowLiveOnOneBranch",
			src: `
				fn go(cond: boolean) -> number {
					val mut p = {x: 0}
					val a = &p
					if cond {
						return a.x
					}
					p.x = 1
					return p.x
				}
			`,
		},
		{
			name: "E2BorrowLiveAcrossLoop",
			src: `
				fn go(items: Array<number>) -> number {
					val mut p = {x: 0}
					val a = &p
					for item in items {
						p.x = item
					}
					return a.x
				}
			`,
			errs: []string{"cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			// d copies a on each iteration, so the loan of p gains d as a holder after the
			// loop's first pass. a still holds the loan after the loop.
			name: "E2WriteWhileCopiedInLoop",
			src: `
				fn go(items: Array<number>) -> number {
					val mut p = {x: 0}
					val a = &p
					for item in items {
						val d = a
						val n = d.x
					}
					p.x = 1
					return a.x
				}
			`,
			errs: []string{"cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			// Each branch reassigns a different holder to a copy of a, so after the join
			// either c or d can hold the borrow of p.
			name: "E2WriteWhileCopiedOnEitherBranch",
			src: `
				fn go(cond: boolean) -> number {
					val mut p = {x: 0}
					val q = {x: 0}
					val a = &p
					var c: &{x: number} = &q
					var d: &{x: number} = &q
					if cond {
						c = a
					} else {
						d = a
					}
					p.x = 1
					return c.x + d.x
				}
			`,
			errs: []string{"cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			// The branch assignment ends the loop body, so its join block has no statements.
			// Whether c is live after the branches then comes from the blocks after the join.
			// c is read on the next iteration after the write of a.x.
			name: "E2WriteWhileBranchAssignedHolderLiveInLoop",
			src: `
				fn go(items: Array<number>, cond: boolean) -> number {
					val mut a = {x: 0}
					val b = {x: 0}
					var c: &{x: number} = &b
					for item in items {
						a.x = item
						val n = c.x
						c = if cond { &a } else { &b }
					}
					return c.x
				}
			`,
			errs: []string{"cannot assign to 'a.x' while it is borrowed as immutable"},
		},
		{
			// The same loop without the reads of c. c is dead after the branch assignment,
			// so the next iteration's write of a.x conflicts with nothing.
			name: "E2WriteWhileBranchAssignedHolderDeadInLoop",
			src: `
				fn go(items: Array<number>, cond: boolean) -> number {
					val mut a = {x: 0}
					val b = {x: 0}
					var c: &{x: number} = &b
					for item in items {
						a.x = item
						c = if cond { &a } else { &b }
					}
					return a.x
				}
			`,
		},
		{
			// A borrow passed as an argument lasts until the end of the call's statement.
			name: "E2WriteAfterArgumentBorrow",
			src: `
				fn peek(r: &{x: number}) -> number {
					return r.x
				}

				fn go() -> number {
					val mut p = {x: 0}
					val n = peek(&p)
					p.x = 1
					return n + p.x
				}
			`,
		},
		{
			name: "E2TwoArgumentBorrowsInOneCall",
			src: `
				fn both(r: &{x: number}, w: &mut {x: number}) -> number {
					return r.x
				}

				fn go() -> number {
					val mut p = {x: 0}
					return both(&p, &mut p)
				}
			`,
			errs: []string{"cannot borrow 'p' as mutable while it is borrowed as immutable"},
		},
		{
			// A borrow stored in a module-level binding stays live at every later point.
			name: "E2WriteWhileModuleBindingHoldsBorrow",
			src: `
				val mut base = {x: 0}
				var g: &{x: number} = &base

				fn go() -> number {
					g = &base
					base.x = 1
					return g.x
				}
			`,
			errs: []string{"cannot assign to 'base.x' while it is borrowed as immutable"},
		},
		{
			name: "E3MoveWhileBorrowed",
			src: `
				fn take(p: {x: number}) -> number {
					val mut q = p
					q.x = 5
					return q.x
				}

				fn go() -> number {
					val p = {x: 1}
					val c = &p
					val n = take(p)
					return c.x + n
				}
			`,
			errs: []string{"cannot move 'p' while it is borrowed"},
		},
		{
			name: "E3MoveAfterBorrowEnds",
			src: `
				fn go() -> number {
					val p = {x: 1}
					val c = &p
					val n = c.x
					val q = p
					return q.x + n
				}
			`,
		},
		{
			name: "E3MoveWhileClosureHoldsBorrow",
			src: `
				fn go() -> number {
					val p = {x: 1}
					val peek = fn () { return p.x }
					val q = p
					return peek()
				}
			`,
			// The first diagnostic is E4's, which the current checks still report.
			errs: []string{
				"this call runs 'peek', which reads 'p' after it was moved",
				"cannot move 'p' while it is borrowed",
			},
		},
		{
			name: "E4WriteBetweenClosureCalls",
			src: `
				fn go() -> number {
					val mut p = {x: 1}
					val peek = fn () { return p.x }
					val a = peek()
					p.x = 2
					return a + peek()
				}
			`,
		},
		{
			// Each branch moves a different element of b, so neither branch moves the whole
			// tuple. The next iteration moves an element a branch moved before.
			name: "TupleElementsMovedOnBranchesInALoop",
			src: `
				fn go(cond: boolean) -> number {
					val b = [{v: 1}, {v: 2}]
					var sum = 0
					for i in [1, 2] {
						if cond {
							val [x, {v}] = b
							sum = sum + v
						} else {
							val [{v}, y] = b
							sum = sum + v
						}
					}
					return sum
				}
			`,
			errs: []string{
				`use of moved value 'b["0"]'`,
				`use of moved value 'b["0"]'`,
			},
		},
		{
			// Each path moves two of the three elements of t, so the read of t sees a partial
			// move rather than a move of the whole tuple.
			name: "TupleElementsMovedOnDifferentPathsArePartialMoves",
			src: `
				fn go(cond: boolean) {
					val t = [{x: 1}, {x: 2}, {x: 3}]
					if cond {
						val [a, {}, {}] = t
					} else {
						val [{}, b, {}] = t
					}
					val [{}, {}, c] = t
					val u = t
				}
			`,
			errs: []string{`use of partially moved value 't'; field 't["0"]' was moved out`},
		},
		{
			// The pass replaces only the diagnostics of E1, E2, and E3, so a type error stays.
			name: "TypeErrorStays",
			src: `
				fn go() -> number {
					val x: number = "a"
					return x
				}
			`,
			errs: []string{`cannot constrain "a" <: number`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := inferWithDataflow(t, tc.src)
			if tc.errs == nil {
				require.Empty(t, errs)
				return
			}
			require.Equal(t, tc.errs, errs)
		})
	}
}

// TestOwnershipDataflowMatchesCurrentChecks covers programs whose diagnostics the dataflow
// pass and the current checks report alike. Each program runs with the flag off and with it
// on, and both runs report want, with spans. The cases cover a call that runs a closure, a
// write through a constant-number index, the span a moved place is blamed at, the name of a
// moved field or tuple, and a write that uses a moved place beside a live borrow.
func TestOwnershipDataflowMatchesCurrentChecks(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			// The call moves f, since f's body moves the capture p. E4 checks the second call.
			name: "ClosureMovingACaptureCalledTwice",
			src: `
				declare fn take(p: {x: number}) -> number
				fn go() -> number {
					val p = {x: 1}
					val f = fn () { return take(p) }
					val a = f()
					return a + f()
				}
			`,
			want: []string{"7:17-7:18: use of moved value 'f'"},
		},
		{
			// Both calls are in one statement, so the second is reported as soon as it moves f.
			name: "ClosureMovingACaptureCalledTwiceInOneStatement",
			src: `
				declare fn take(p: {x: number}) -> number
				fn go() -> number {
					val p = {x: 1}
					val f = fn () { return take(p) }
					return f() + f()
				}
			`,
			want: []string{"6:19-6:22: use of moved value 'f'"},
		},
		{
			// The closure's write to x is live for the call that receives it, beside a.
			name: "ClosureArgumentBesideAnImmutableBorrow",
			src: `
				declare fn run(a: &{v: number}, f: fn () -> undefined) -> undefined
				fn go() {
					val mut x = {v: 1}
					val a = &x
					run(a, fn () { x.v = 5 })
				}
			`,
			want: []string{"6:13-6:30: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		{
			name: "WriteThroughAnIndexNamesTheIndex",
			src: `
				fn go() -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					val r = &xs[0]
					xs[1].x = 3
					return r.x
				}
			`,
			want: []string{"5:6-5:13: cannot assign to 'xs[1].x' while it is borrowed as immutable"},
		},
		{
			name: "WriteAfterMoveBlamesTheMovedPlace",
			src: `
				fn go(mut p: {x: number}) {
					val q: {x: number} = p
					p.x = 5
					q.x
				}
			`,
			want: []string{"4:6-4:7: use of moved value 'p'"},
		},
		{
			name: "BorrowAfterMoveBlamesTheOperand",
			src: `
				declare fn sink(p: {x: number}) -> undefined
				fn go() {
					val a = {x: 1}
					sink(a)
					val r = &a
					r.x
				}
			`,
			want: []string{"6:15-6:16: use of moved value 'a'"},
		},
		{
			name: "MethodCallAfterMoveBlamesTheReceiver",
			src: `
				class C {
					v: number,
					look(&self) -> number { return self.v },
					take(self) -> number { return self.v },
				}
				fn go() -> number {
					val c = C(1)
					c.take()
					return c.look()
				}
			`,
			want: []string{"10:13-10:14: use of moved value 'c'"},
		},
		{
			name: "DestructuringWhileBorrowedNamesTheMovedField",
			src: `
				fn go() -> number {
					val p = {f: {x: 1}}
					val r = &p
					val {f: d} = p
					return r.f.x
				}
			`,
			want: []string{"5:19-5:20: cannot move 'p.f' while it is borrowed"},
		},
		{
			name: "DestructuringInALoopNamesTheMovedField",
			src: `
				fn go() {
					val p = {f: {x: 1}}
					for i in [1, 2] {
						val {f: d} = p
					}
				}
			`,
			want: []string{"5:20-5:21: use of moved value 'p.f'"},
		},
		{
			name: "TupleWithEveryElementMovedIsMoved",
			src: `
				fn go() {
					val b = [{v: 1}, {v: 2}]
					val [x, y] = b
					val z = b
				}
			`,
			want: []string{"5:14-5:15: use of moved value 'b'"},
		},
		{
			name: "TupleRestMovesTheTuple",
			src: `
				fn go() {
					val b = [{v: 1}, {v: 2}]
					val [x, ...rest] = b
					val z = b
				}
			`,
			want: []string{"5:14-5:15: use of moved value 'b'"},
		},
		{
			// Each element moves again on the next iteration, and the destructuring is
			// reported once.
			name: "TupleDestructuredInALoopReportsOnce",
			src: `
				fn go() {
					val b = [{x: 1}, {x: 2}]
					for i in [1, 2] {
						val [x, y] = b
					}
				}
			`,
			want: []string{"5:20-5:21: use of moved value 'b'"},
		},
		{
			// The destructuring moves p.f and p.g, and is reported once.
			name: "DestructuringTwoFieldsWhileBorrowedReportsOnce",
			src: `
				fn go() -> number {
					val p = {f: {x: 1}, g: {x: 2}}
					val r = &p
					val {f: d, g: e} = p
					return r.f.x
				}
			`,
			want: []string{"5:25-5:26: cannot move 'p.f' while it is borrowed"},
		},
		{
			// A dynamic index adds no segment to the place, so the write blames `xs[i]`.
			name: "WriteThroughADynamicIndexAfterMoveBlamesTheElement",
			src: `
				declare fn sink(p: Array<{x: number}>) -> undefined
				fn go(i: number) {
					val mut xs: Array<{x: number}> = [{x: 1}]
					sink(xs)
					xs[i].x = 2
				}
			`,
			want: []string{"6:6-6:11: use of moved value 'xs'"},
		},
		{
			// The write uses a moved a, so it reports E1 and not E2 beside the live snapshot.
			name: "WriteToAMovedPlaceReportsOnlyTheMove",
			src: `
				declare fn sink(p: {x: number}) -> undefined
				fn go() {
					val mut a: {x: number} = {x: 1}
					sink(a)
					val snapshot: &{x: number} = &a
					a.x = 2
					snapshot
				}
			`,
			want: []string{
				"6:36-6:37: use of moved value 'a'",
				"7:6-7:7: use of moved value 'a'",
			},
		},
		{
			// a moves on one branch only. The write still reports only the move.
			name: "WriteToAPlaceMovedOnOneBranchReportsOnlyTheMove",
			src: `
				declare fn sink(p: {x: number}) -> undefined
				fn go(cond: boolean) {
					val mut a: {x: number} = {x: 1}
					if cond {
						sink(a)
					}
					val snapshot: &{x: number} = &a
					a.x = 2
					snapshot
				}
			`,
			want: []string{
				"8:36-8:37: use of moved value 'a'",
				"9:6-9:7: use of moved value 'a'",
			},
		},
	}

	for _, tc := range cases {
		for _, dataflow := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dataflow=%t", tc.name, dataflow), func(t *testing.T) {
				res := InferModuleAgainstStdlib(parseModule(t, tc.src), benchTree, Options{OwnershipDataflow: dataflow})
				require.Equal(t, tc.want, messagesWithSpan(t, res.Errors))
			})
		}
	}
}
