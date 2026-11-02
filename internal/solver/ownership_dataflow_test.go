package solver

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOwnershipDataflow covers E1, E2, and E3 as the dataflow pass checks them: a program each rule
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
			// The move is reported once, at the move. The current checks' E4 report of the later
			// call is dropped, since it names the same move.
			errs: []string{"cannot move 'p' while it is borrowed"},
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
			// The closure in sink borrows p for `'static`, so p cannot move even though the
			// closure only reads it. The closure rows of the exclusivity table reject a move
			// between calls.
			name: "E3MoveAfterStoringAReadingClosureIntoAGlobal",
			src: `
				var sink: fn () -> number = fn () { return 0 }
				declare fn take(x: {x: number}) -> undefined
				fn go() {
					val p = {x: 1}
					sink = fn () { return p.x }
					take(p)
				}
			`,
			errs: []string{"cannot move 'p' while it is borrowed"},
		},
		{
			// A closure stored into sink and also returned keeps its `'static` borrow of p, so
			// the return does not move p. A later move of p conflicts with that borrow. The
			// first diagnostic is the current checks' escape check of `return f`, which the
			// pass does not replace.
			name: "E3MoveAfterStoringAndReturningAClosure",
			src: `
				var sink: fn () -> number = fn () { return 0 }
				declare fn take(x: {x: number}) -> undefined
				fn go(c: boolean) {
					val p = {x: 1}
					val f = fn () { return p.x }
					sink = f
					if c {
						return f
					}
					take(p)
					return fn () { return 0 }
				}
			`,
			errs: []string{
				"'p' leaves the function both as an owned value and through a borrow",
				"cannot move 'p' while it is borrowed",
			},
		},
		{
			// One return that names the closure twice moves its capture once.
			name: "ReturnOfAClosureTwiceInOneValue",
			src: `
				fn go() {
					val p = {x: 1}
					val f = fn () { return p.x }
					return [f, f]
				}
			`,
		},
		{
			// The return of f moves p while `a` still borrows it for the returned tuple.
			name: "E3ReturnOfAClosureBesideABorrowOfItsCapture",
			src: `
				fn go() {
					val p = {x: 1}
					val f = fn () { return p.x }
					val a = &p
					return [f, a]
				}
			`,
			errs: []string{"cannot move 'p' while it is borrowed"},
		},
		{
			// f holds another closure by the return, so the return does not move p.
			name: "ReturnOfAReassignedClosureMovesNothing",
			src: `
				fn go() {
					val p = {x: 1}
					var f: fn () -> number = fn () { return p.x }
					f = fn () { return 0 }
					val a = &p
					return [f, a]
				}
			`,
		},
		{
			// The return inside the branch returns the closure that captures nothing, so it
			// does not move p before `p` is read.
			name: "ReturnOfAClosureReassignedInABranchMovesNothing",
			src: `
				fn go(c: boolean) {
					val p = {x: 1}
					var f: fn () -> number = fn () { return p.x }
					if c {
						f = fn () { return 0 }
						return [f, p]
					}
					return [f, f]
				}
			`,
		},
		{
			// f's closure flows into g before f is reassigned, so the return of g moves p.
			name: "E3ReturnOfAClosureCopiedBeforeItsLocalIsReassigned",
			src: `
				fn go() {
					val p = {x: 1}
					var f: fn () -> number = fn () { return p.x }
					val g = f
					f = fn () { return 0 }
					val a = &p
					return [g, a]
				}
			`,
			errs: []string{"cannot move 'p' while it is borrowed"},
		},
		{
			// f borrows r for `'static` once it is stored into sink, so the borrow of p that r
			// holds lasts for `'static` as well.
			name: "E3MoveAfterStoringAClosureThatCapturesABorrow",
			src: `
				declare fn take(p: {x: number}) -> number
				var sink: fn () -> number = fn () { return 0 }
				fn go() {
					val p = {x: 1}
					val r = &p
					val f = fn () { return r.x }
					val n = r.x
					sink = f
					take(p)
				}
			`,
			errs: []string{"cannot move 'p' while it is borrowed"},
		},
		{
			// The move conflicts with a and with f's capture. It is reported once, at the move,
			// and the current checks' E4 report of the later call is dropped.
			name: "E3MoveWhileABorrowAndAClosureHoldP",
			src: `
				declare fn take(p: {x: number}) -> number
				declare fn read(a: &{x: number}) -> number
				fn go() {
					val p = {x: 1}
					val a = &p
					val f = fn () { return p.x }
					take(p)
					read(a)
					f()
				}
			`,
			errs: []string{"cannot move 'p' while it is borrowed"},
		},
		{
			// The receiver is evaluated before the argument, so the argument's borrow conflicts.
			name: "ReceiverBorrowedBeforeTheArgument",
			src: `
				class R {
					x: number,
					readWith(&self, w: &mut R) -> undefined {},
				}
				fn go() {
					val mut p = R(1)
					p.readWith(&mut p)
				}
			`,
			errs: []string{"cannot borrow 'p' as mutable while it is borrowed as immutable"},
		},
		{
			// The leaf d borrows p.f for as long as d is used.
			name: "PatternOverAWrittenBorrowHoldsTheBorrow",
			src: `
				fn go() -> number {
					val mut p = {f: {x: 1}}
					val {f: d} = &p
					p.f.x = 2
					return d.x
				}
			`,
			errs: []string{"cannot assign to 'p.f.x' while it is borrowed as immutable"},
		},
		{
			// The closure moves only p.a, so p.b stays usable.
			name: "ClosureMovingAFieldLeavesTheSiblingUsable",
			src: `
				fn go() -> number {
					val p = {a: {x: 1}, b: {x: 2}}
					val f = fn () {
						val q = p.a
						return q.x
					}
					val n = f()
					return n + p.b.x
				}
			`,
		},
		{
			name: "ClosureMovingAFieldPartiallyMovesTheLocal",
			src: `
				fn go() {
					val p = {a: {x: 1}, b: {x: 2}}
					val f = fn () {
						val q = p.a
						return q.x
					}
					val n = f()
					val e = p
				}
			`,
			errs: []string{"use of partially moved value 'p'; field 'p.a' was moved out"},
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
				`use of moved value 'b[0]'`,
				`use of moved value 'b[0]'`,
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
			errs: []string{`use of partially moved value 't'; field 't[0]' was moved out`},
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
		{
			// The first program of B2. The global keeps the `'static` borrow of opts live for
			// the rest of the program, so the write breaks E2.
			name: "E2WriteAfterAStaticImmutableBorrow",
			src: `
				var cache: &{retries: number} = &{retries: 0}

				export fn go() -> number {
					val mut opts = {retries: 3}
					cache = &opts
					opts.retries = 4
					return opts.retries
				}
			`,
			errs: []string{"cannot assign to 'opts.retries' while it is borrowed as immutable"},
		},
		{
			// The second program of B2. The `'static` parameter takes the borrow's lifetime, so
			// the mutable borrow stays live after the call.
			name: "E2ImmutableBorrowAfterAStaticMutableBorrow",
			src: `
				declare fn track(value: &'static mut {retries: number}) -> undefined
				declare fn read(value: &{retries: number}) -> number

				export fn go() -> number {
					val mut opts = {retries: 3}
					track(&mut opts)
					return read(&opts)
				}
			`,
			errs: []string{"cannot borrow 'opts' as immutable while it is borrowed as mutable"},
		},
		{
			// Copying the borrow m into a `'static` parameter derives a `'static` loan of m.
			name: "E2CopyIntoAStaticParameterStaysLive",
			src: `
				declare fn track(value: &'static mut {retries: number}) -> undefined
				declare fn read(value: &{retries: number}) -> number

				export fn go(m: &mut {retries: number}) -> number {
					track(m)
					return read(m)
				}
			`,
			errs: []string{"cannot borrow 'm' as immutable while it is borrowed as mutable"},
		},
		{
			// The store on one branch keeps the borrow live after the join.
			name: "E2StaticBorrowOnOneBranch",
			src: `
				declare var sink: &{x: number}
				fn go(c: boolean) -> number {
					val mut p = {x: 1}
					if c {
						sink = &p
					}
					p.x = 2
					return p.x
				}
			`,
			errs: []string{"cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			// z copies the parameter m with its own kind, which makes no new path, so passing
			// m to a `&` parameter while z is live is allowed.
			name: "CopyOfAParameterWithItsKindOk",
			src: `
				declare fn read(a: &{x: number}) -> number
				fn go(m: &mut {x: number}) -> number {
					val z = m
					val n = read(m)
					z.x = 1
					return n
				}
			`,
		},
		{
			// s narrows m to `&` and is read after the call, so passing m to a `&mut`
			// parameter makes a mutable path while the immutable one is live.
			name: "E2NarrowedCopyLiveAtAMutableUse",
			src: `
				declare fn write(a: &mut {x: number}) -> undefined
				fn go(m: &mut {x: number}) -> number {
					val s: &{x: number} = m
					write(m)
					return s.x
				}
			`,
			errs: []string{"cannot borrow 'm' as mutable while it is borrowed as immutable"},
		},
		{
			// c narrows the borrow b holds and is read after the write through b.
			name: "E2WriteThroughTheSourceOfANarrowedCopy",
			src: `
				fn go() -> number {
					val mut a = {x: 1}
					val b: &mut {x: number} = &mut a
					val c: &{x: number} = b
					b.x = 2
					return c.x
				}
			`,
			errs: []string{"cannot assign to 'b.x' while it is borrowed as immutable"},
		},
		{
			// r borrows h's storage, and c copies the borrow the slot h.s holds, so c reaches x
			// and not h.
			name: "CopyOutOfAMutablyBorrowedSlotOk",
			src: `
				fn go() {
					val x = {v: 1}
					val mut h: {s: &{v: number}} = {s: &x}
					val r = &mut h
					val c = h.s
					r
					c
				}
			`,
		},
		{
			// s is not read after the call, so its immutable path has ended.
			name: "NarrowedCopyDeadAtAMutableUseOk",
			src: `
				declare fn write(a: &mut {x: number}) -> undefined
				fn go(m: &mut {x: number}) -> number {
					val s: &{x: number} = m
					val n = s.x
					write(m)
					return n
				}
			`,
		},
		{
			// a's immutable borrow of b is live through the return, so `&mut b` breaks E2.
			// The frame-exit check finds the same pair of paths and is not reported again.
			name: "E2AtAReturnReplacesTheSharedPathsCheck",
			src: `
				fn build() -> [&{value: number}, &mut {value: number}] {
					val mut b = {value: 2}
					val mut a = {peer: &b}
					return [a.peer, &mut b]
				}
			`,
			errs: []string{"cannot borrow 'b' as mutable while it is borrowed as immutable"},
		},
		{
			// The store's `&b` breaks E2 against a's mutable borrow, which the return keeps
			// live. The frame-exit check pairs the same store with the return.
			name: "E2AtAStoreReplacesTheMixedOutflowCheck",
			src: `
				fn f(p: &mut {r: &{value: number}}) -> {peer: &mut {value: number}} {
					val mut b = {value: 1}
					val a = {peer: &mut b}
					p.r = &b
					return a
				}
			`,
			errs: []string{"cannot borrow 'b' as immutable while it is borrowed as mutable"},
		},
		{
			// The caller reads p.r only after the return, when a is dead, so `&b.x` breaks no
			// rule. The caller reaches both `b.y` borrows, so the pass reports E2 at the store
			// into q. That pair is the one the frame-exit check reports, so the frame-exit
			// diagnostic is dropped.
			name: "E2OnTheSamePairReplacesTheMixedOutflowCheck",
			src: `
				fn f(p: &mut {r: {x: &{v: number}, y: &{v: number}}}, q: &mut {r: &mut {v: number}}) {
					val mut b = {x: {v: 1}, y: {v: 2}}
					val a = &mut b.x
					p.r = {x: &b.x, y: &b.y}
					a.v = 3
					q.r = &mut b.y
				}
			`,
			errs: []string{"cannot borrow 'b.y' as mutable while it is borrowed as immutable"},
		},
		{
			// The body reads `p.r` while a's mutable borrow is live, so the store's `&b` breaks
			// E2. The current checks report that store with a message naming a, which takes the
			// place of the pass's. The frame-exit check reports that b leaves both owned and
			// through a borrow, which is not a pair E2 covers, so it stays.
			name: "AnOwnedOutflowStaysBesideE2",
			src: `
				fn f(p: &mut {r: &{v: number}}) -> {v: number} {
					val mut b = {v: 1}
					val a = &mut b
					p.r = &b
					a.v = p.r.v + 1
					return b
				}
			`,
			errs: []string{
				"cannot store an immutable borrow of 'b' while 'a' still borrows it as mutable",
				"'b' leaves the function both as an owned value and through a borrow",
			},
		},
		{
			// t holds both borrows one literal takes, and the return uses t, so the pass reports
			// the pair by E2. The frame-exit diagnostic at the return t flows into is dropped.
			name: "SharedPathsInOneLiteralReportedByE2",
			src: `
				fn build() {
					val mut b = {x: {n: 1}, y: {n: 2}}
					val t = {p: &mut b.x, q: &b.x}
					return t
				}
			`,
			errs: []string{"cannot borrow 'b.x' as immutable while it is borrowed as mutable"},
		},
		{
			// The pass reports E2 for c inside the return, which replaces the frame-exit
			// diagnostic about c. The borrow of b that take receives ends with the call, so
			// the pass reports nothing about b, and the frame-exit diagnostic about b stays.
			name: "E2OnAnotherLocalKeepsTheSharedPathsCheck",
			src: `
				declare fn take(x: {peer: &{n: number}}) -> undefined
				fn build() {
					val mut b = {n: 1}
					val mut c = {n: 3}
					take({peer: &b})
					return [&mut b, &c, &mut c]
				}
			`,
			errs: []string{
				"'b' leaves the function through a mutable path and an immutable one",
				"cannot borrow 'c' as mutable while it is borrowed as immutable",
			},
		},
		{
			// The first argument passes m to a `&` parameter while the second passes it to one
			// with a `&mut` member, which the callee can narrow to and write through.
			name: "E2MixedUnionArgumentAfterAnImmutableOne",
			src: `
				declare fn readWrite(a: &{x: number}, b: &mut {x: number} | &{x: number}) -> undefined
				fn go(m: &mut {x: number} | &{x: number}) {
					readWrite(m, m)
				}
			`,
			errs: []string{"cannot borrow 'm' as mutable while it is borrowed as immutable"},
		},
		{
			name: "E2MixedUnionArgumentBeforeAnImmutableOne",
			src: `
				declare fn writeRead(a: &mut {x: number} | &{x: number}, b: &{x: number}) -> undefined
				fn go(m: &mut {x: number} | &{x: number}) {
					writeRead(m, m)
				}
			`,
			errs: []string{"cannot borrow 'm' as immutable while it is borrowed as mutable"},
		},
		{
			// The first argument copies m with its own kind, so it is the `&mut` path the
			// second argument's `&` path conflicts with.
			name: "E2ImmutableArgumentAfterAMutableCopyOfAParameter",
			src: `
				declare fn writeRead(a: &mut {x: number}, b: &{x: number}) -> undefined
				fn go(m: &mut {x: number}) {
					writeRead(m, m)
				}
			`,
			errs: []string{"cannot borrow 'm' as immutable while it is borrowed as mutable"},
		},
		{
			name: "E2ImmutableArgumentAfterAMutableCopyOfALocalBorrow",
			src: `
				declare fn writeRead(a: &mut {x: number}, b: &{x: number}) -> undefined
				fn go() {
					val mut x = {x: 1}
					val m = &mut x
					writeRead(m, m)
				}
			`,
			errs: []string{"cannot borrow 'm' as immutable while it is borrowed as mutable"},
		},
		{
			// d is not used after the copy, but its mutable path to p is `'static`, so p never
			// gets a new immutable path.
			name: "E2NarrowedCopyOfAStaticMutableBorrow",
			src: `
				fn go() -> number {
					val mut p = {x: 1}
					val d: &'static mut {x: number} = &mut p
					val r: &{x: number} = d
					return r.x
				}
			`,
			errs: []string{"cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		{
			// Each iteration copies a into a `'static` destination. The copy derives one loan
			// from p's loan however many times the loop runs, so the pass reaches a fixed point.
			name: "StaticSelfCopyInALoopOk",
			src: `
				fn go(p: &'static {x: number}, items: Array<number>) {
					var a: &'static {x: number} = p
					for item in items {
						a = a
					}
				}
			`,
		},
		{
			// The pass reports E2 for b at `&mut b`, which lies outside both the store `q.s = c`
			// that the frame-exit diagnostic blames and the store `p.r = a` it relates. So the
			// frame-exit diagnostic about b stays beside the E2.
			name: "E2OutsideTheOutflowSpansKeepsTheSharedPathsCheck",
			src: `
				fn f(p: &mut {r: &{v: number}}, q: &mut {s: &mut {v: number}}) {
					val mut b = {v: 1}
					val a = &b
					val c = &mut b
					p.r = a
					q.s = c
				}
			`,
			errs: []string{
				"cannot borrow 'b' as mutable while it is borrowed as immutable",
				"'b' leaves the function through a mutable path and an immutable one",
			},
		},
		{
			// The annotation gives the literal's element a `'static` borrow, so it is live at the
			// write.
			name: "StaticBorrowInATypedLiteral",
			src: `
				fn go() {
					val mut p = {x: 1}
					val o: {f: &'static {x: number}} = {f: &p}
					p.x = 2
				}
			`,
			errs: []string{"cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			// The pattern's annotation is `'static`, so the leaf d borrows p.f for `'static`.
			name: "StaticBorrowInAnAnnotatedPattern",
			src: `
				fn go() {
					val mut p = {f: {x: 1}}
					val {f: d}: &'static {f: {x: number}} = &p
					p.f.x = 2
				}
			`,
			errs: []string{"cannot assign to 'p.f.x' while it is borrowed as immutable"},
		},
		{
			// The annotation gives each element of the array literal a `'static` borrow.
			name: "StaticBorrowInATypedArrayLiteral",
			src: `
				fn go() {
					val mut p = {x: 1}
					val a: Array<&'static {x: number}> = [&p]
					p.x = 2
				}
			`,
			errs: []string{"cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			// The element type is `'static`, so the stored borrow stays live after the store.
			name: "StaticBorrowStoredIntoAnElement",
			src: `
				fn go(arr: &mut Array<&'static mut {x: number}>) {
					val mut p = {x: 1}
					arr[0] = &mut p
					val r = &p
				}
			`,
			errs: []string{"cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		{
			// The element type is not `'static`, so the stored borrow ends where the body stops
			// reading arr.
			name: "BorrowStoredIntoAnElementEndsOk",
			src: `
				fn go(arr: &mut Array<&mut {x: number}>) {
					val mut p = {x: 1}
					arr[0] = &mut p
					val r = &p
				}
			`,
		},
		{
			// The inner call's receiver borrow ends when `v.len()` returns, and the outer
			// `&mut self` receiver only takes effect once its arguments are evaluated.
			name: "NestedReceiverCallEndsItsBorrowOk",
			src: `
				class V {
					n: number,
					len(&self) -> number { return self.n },
					push(&mut self, x: number) -> undefined {},
				}
				fn go() {
					val mut v = V(1)
					v.push(v.len())
				}
			`,
		},
		{
			// The `&mut self` receiver takes effect when the call starts, while the argument
			// `&p` is still live, so the argument is blamed.
			name: "MutableReceiverConflictsWithAnArgument",
			src: `
				class R {
					x: number,
					writeRead(&mut self, r: &R) -> undefined {},
				}
				fn go() {
					val mut p = R(1)
					p.writeRead(&p)
				}
			`,
			errs: []string{"cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		{
			// An argument's borrow ends when its call returns.
			name: "ArgumentBorrowEndsWithItsCallOk",
			src: `
				declare fn bump(p: &mut {x: number}) -> number
				declare fn look(n: number, r: &{x: number}) -> number
				fn go() -> number {
					val mut a = {x: 1}
					return look(bump(&mut a), &a)
				}
			`,
		},
		{
			// pickMut returns the borrow of its argument, which the outer call receives, so
			// the borrow lasts until the outer call returns.
			name: "ReturnedBorrowPassedToAnotherCall",
			src: `
				declare fn pickMut<'a>(p: &'a mut {x: number}) -> &'a mut {x: number}
				declare fn both(m: &mut {x: number}, r: &{x: number}) -> undefined
				fn go() {
					val mut a = {x: 1}
					both(pickMut(&mut a), &a)
				}
			`,
			errs: []string{"cannot borrow 'a' as immutable while it is borrowed as mutable"},
		},
		{
			// A store ties the item to the target's field, so the field holds the item's borrow
			// while the target is used.
			name: "StoreEdgeKeepsTheItemBorrowed",
			src: `
				declare fn store<'a>(target: &mut {peer: &'a mut {x: number}}, item: &'a mut {x: number}) -> undefined
				fn go(mut q: {x: number}) -> number {
					val mut a = {peer: &mut q}
					val mut b = {x: 1}
					store(&mut a, &mut b)
					val c = b
					return a.peer.x
				}
			`,
			errs: []string{"cannot move 'b' while it is borrowed"},
		},
		{
			// c takes b's borrows field by field, so writing `c.other` leaves the loan of d
			// that `c.peer` holds.
			name: "WritingASiblingFieldKeepsTheLoan",
			src: `
				fn go() {
					val mut d = {value: 1}
					val mut e = {value: 2}
					val mut g = {value: 3}
					val b = {peer: &mut d, other: &mut e}
					var mut c = b
					c.other = &mut g
					val y = d
					val z = c
				}
			`,
			errs: []string{"cannot move 'd' while it is borrowed"},
		},
		{
			// The caller reads `p.r` only once f returns, after the last write of b.
			name: "WriteBeforeTheCallerReadsAStoredBorrowOk",
			src: `
				fn f(p: &mut {r: &{value: number}}, xs: [number]) {
					val mut b = {value: 1}
					for x in xs {
						b.value = x
						p.r = &b
					}
				}
			`,
		},
		{
			// The caller runs at the `yield`, and can keep a copy of `p.r`, so the stored
			// borrow stays live after it.
			name: "WriteAfterAYieldConflicts",
			src: `
				gen fn f(p: &mut {r: &{value: number}}) {
					val mut b = {value: 1}
					p.r = &b
					yield 1
					b.value = 5
				}
			`,
			errs: []string{"cannot assign to 'b.value' while it is borrowed as immutable"},
		},
		{
			// A rest parameter receives every argument from its position on, so each of them
			// passes its borrow to the result.
			name: "RestArgumentsPassTheirBorrowsToTheResult",
			src: `
				declare fn pickAny<'a>(...ps: Array<&'a {x: number}>) -> &'a {x: number}
				fn go() -> number {
					val mut p = {x: 1}
					val mut q = {x: 1}
					val d = pickAny(&p, &q)
					q.x = 2
					return d.x
				}
			`,
			errs: []string{"cannot assign to 'q.x' while it is borrowed as immutable"},
		},
		{
			// a borrows x, so the store through a lands in `x.peer`, which the return reads.
			name: "StoreThroughABorrowingLocalReachesWhatItBorrows",
			src: `
				declare fn store<'a>(target: &mut {peer: &'a {x: number}}, item: &'a {x: number}) -> undefined
				fn go() -> number {
					val mut x = {peer: &{x: 0}}
					val mut b = {x: 1}
					val a = &mut x
					store(a, &b)
					b.x = 2
					return x.peer.x
				}
			`,
			errs: []string{"cannot assign to 'b.x' while it is borrowed as immutable"},
		},
		{
			// A field of a call's result holds what the result borrows at that field.
			name: "FieldOfACallResultHoldsItsBorrow",
			src: `
				declare fn wrap<'a>(p: &'a {x: number}) -> {inner: &'a {x: number}, n: number}
				fn go() -> number {
					val mut p = {x: 1}
					val d = wrap(&p).inner
					p.x = 2
					return d.x
				}
			`,
			errs: []string{"cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			// d reads only the `inner` field of the result, which holds p's borrow and not q's.
			name: "FieldOfACallResultHoldsOnlyItsOwnBorrowOk",
			src: `
				declare fn wrap2<'a, 'b>(p: &'a {x: number}, q: &'b {x: number}) -> {inner: &'a {x: number}, other: &'b {x: number}}
				fn go() -> number {
					val mut p = {x: 1}
					val mut q = {x: 1}
					val d = wrap2(&p, &q).inner
					q.x = 2
					return d.x
				}
			`,
		},
		{
			// A value-type field of a call's result holds no borrow.
			name: "ValueFieldOfACallResultOk",
			src: `
				declare fn wrap<'a>(p: &'a {x: number}) -> {inner: &'a {x: number}, n: number}
				fn go() -> number {
					val mut p = {x: 1}
					val n = wrap(&p).n
					p.x = 2
					return n
				}
			`,
		},
		{
			// r is used only while the arguments are evaluated, so it is dead once the
			// `&mut self` receiver takes effect.
			name: "ImmutableBorrowUsedOnlyInTheArgumentsOk",
			src: `
				class V {
					n: number,
					len(&self) -> number { return self.n },
					push(&mut self, x: number) -> undefined {},
				}
				fn go() {
					val mut v = V(1)
					val r = &v
					v.push(r.len())
				}
			`,
		},
		{
			// r is read later in the same statement, after the `&mut self` call, so the receiver
			// breaks E2.
			name: "ImmutableBorrowReadLaterInTheStatement",
			src: `
				declare fn both(u: undefined, n: number) -> number
				class V {
					n: number,
					push(&mut self, x: number) -> undefined {},
				}
				fn go() -> number {
					val mut v = V(1)
					val r = &v
					return both(v.push(1), r.n)
				}
			`,
			errs: []string{"cannot borrow 'v' as mutable while it is borrowed as immutable"},
		},
		{
			// r is used after the call, so the `&mut self` receiver breaks E2 and is blamed.
			name: "ImmutableBorrowLiveAfterAMutableReceiver",
			src: `
				class V {
					n: number,
					push(&mut self, x: number) -> undefined {},
				}
				fn go() -> number {
					val mut v = V(1)
					val r = &v
					v.push(1)
					return r.n
				}
			`,
			errs: []string{"cannot borrow 'v' as mutable while it is borrowed as immutable"},
		},
		{
			// The write comes before the `yield`, where the caller first reads `p.r`.
			name: "WriteBeforeAYieldOk",
			src: `
				gen fn f(p: &mut {r: &{value: number}}) {
					val mut b = {value: 1}
					p.r = &b
					b.value = 5
					yield 1
				}
			`,
		},
		{
			// get returns the borrow of p that h holds through 'a, so x holds that borrow and
			// not the receiver's own `&self` borrow of h.
			name: "NestedReturnLeavesTheReceiverWritable",
			src: `
				class Holder<'a> {
					peer: &'a {x: number},
					n: number,
					get(&self) -> &'a {x: number} { return self.peer },
				}
				fn go() -> number {
					val p = {x: 1}
					val mut h = Holder(&p, 0)
					val x = h.get()
					h.n = 5
					return x.x
				}
			`,
		},
		{
			name: "NestedReturnLeavesTheReceiverMovable",
			src: `
				class Holder<'a> {
					peer: &'a {x: number},
					n: number,
					get(&self) -> &'a {x: number} { return self.peer },
				}
				fn go() -> number {
					val p = {x: 1}
					val mut h = Holder(&p, 0)
					val x = h.get()
					val y = h
					return x.x
				}
			`,
		},
		{
			// x holds the borrow of p that h holds through 'a.
			name: "NestedReturnKeepsTheHeldBorrow",
			src: `
				class Holder<'a> {
					peer: &'a {x: number},
					n: number,
					get(&self) -> &'a {x: number} { return self.peer },
				}
				fn go() -> number {
					val p = {x: 1}
					val h = Holder(&p, 0)
					val x = h.get()
					val q = p
					return x.x
				}
			`,
			errs: []string{"cannot move 'p' while it is borrowed"},
		},
		{
			// h holds a mutable borrow of p through 'a. get cannot copy it out of the `&self`
			// borrow, so x reborrows it through that borrow and keeps h borrowed.
			name: "NestedMutableReturnKeepsTheReceiverBorrowed",
			src: `
				class Holder<'a> {
					peer: &'a mut {x: number},
					n: number,
					get(&self) -> &'a mut {x: number} { return self.peer },
				}
				fn go() -> number {
					val mut p = {x: 1}
					val h = Holder(&mut p, 0)
					val x = h.get()
					val y = h
					return x.x
				}
			`,
			errs: []string{"cannot move 'h' while it is borrowed"},
		},
		{
			// get shares the receiver's own borrow lifetime with its result, so x borrows h.
			name: "DirectReturnKeepsTheReceiverBorrowed",
			src: `
				class Box {
					inner: {x: number},
					get<'h>(&'h self) -> &'h {x: number} { return &self.inner },
				}
				fn go() -> number {
					val b = Box({x: 1})
					val x = b.get()
					val y = b
					return x.x
				}
			`,
			errs: []string{"cannot move 'b' while it is borrowed"},
		},
		{
			// copyTo stores the borrow of p that h holds through 'a into out.peer, so out
			// holds that borrow and not the receiver's own `&self` borrow of h.
			name: "NestedStoreLeavesTheReceiverWritable",
			src: `
				class Holder<'a> {
					peer: &'a {x: number},
					n: number,
					copyTo(&self, out: &mut {peer: &'a {x: number}}) -> undefined {
						out.peer = self.peer
					},
				}
				fn go() -> number {
					val p = {x: 1}
					val q = {x: 2}
					val mut h = Holder(&p, 0)
					val mut out = {peer: &q}
					h.copyTo(&mut out)
					h.n = 5
					return out.peer.x
				}
			`,
		},
		{
			// out.peer holds the borrow of p that h holds through 'a.
			name: "NestedStoreKeepsTheHeldBorrow",
			src: `
				class Holder<'a> {
					peer: &'a {x: number},
					n: number,
					copyTo(&self, out: &mut {peer: &'a {x: number}}) -> undefined {
						out.peer = self.peer
					},
				}
				fn go() -> number {
					val p = {x: 1}
					val q = {x: 2}
					val h = Holder(&p, 0)
					val mut out = {peer: &q}
					h.copyTo(&mut out)
					val r = p
					return out.peer.x
				}
			`,
			errs: []string{"cannot move 'p' while it is borrowed"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := inferAgainstTree(t, tc.src)
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
// write through a `number`-typed index, the span a moved place is blamed at, the name of a
// moved field or tuple, a write that uses a moved place beside a live borrow, and a borrow
// copied into a second binding.
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
				fn go(i: number) -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					val r = &xs[0]
					xs[i].x = 3
					return r.x
				}
			`,
			want: []string{"5:6-5:13: cannot assign to 'xs[i].x' while it is borrowed as immutable"},
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
			// A dynamic index names the whole container, so the write blames `xs[i]`.
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
		{
			// The field c.f holds a borrow, so the pattern copies it and c stays usable.
			name: "PatternOverABorrowFieldCopies",
			src: `
				fn go(c: {f: &{x: number}}) -> number {
					val {f: d} = c
					val {f} = c
					return d.x + f.x + c.f.x
				}
			`,
		},
		{
			// The missing `&` is the one report. The binding neither borrows p nor moves it,
			// so the move into take reports nothing.
			name: "MissingBorrowInABindingReportsOnce",
			src: `
				declare fn take(p: {x: number}) -> number
				fn go() -> number {
					val p = {x: 1}
					val c: &{x: number} = p
					val n = take(p)
					return c.x + n
				}
			`,
			want: []string{"5:28-5:29: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		{
			name: "MissingBorrowInALiteralElementReportsOnce",
			src: `
				declare fn hold(h: {slot: &{x: number}}) -> undefined
				fn go() -> number {
					val p = {x: 1}
					hold({slot: p})
					return p.x
				}
			`,
			want: []string{"5:18-5:19: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		{
			name: "MissingBorrowInABranchReportsOnce",
			src: `
				fn go() {
					val mut items: {x: number} = {x: 1}
					val snapshot: &{x: number} = do { items }
					items.x = 2
					snapshot
				}
			`,
			want: []string{"4:40-4:45: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		{
			// The rest element is rejected, so the pattern moves nothing out of p.
			name: "RejectedRestPatternMovesNothing",
			src: `
				fn go(p: {x: number, y: string}) {
					val {...rest, x} = p
					return [rest, p.y]
				}
			`,
			want: []string{"3:11-3:18: Unsupported: ObjRestPat"},
		},
		{
			name: "OverloadedConsumingMethodMovesTheReceiver",
			src: `
				class D {
					v: number,
					take(self, x: number) -> number { return x },
					take(self, x: string) -> string { return x },
				}
				fn go() -> number {
					val d = D(1)
					d.take(1)
					return d.v
				}
			`,
			want: []string{"10:13-10:16: use of moved value 'd'"},
		},
		{
			name: "ConsumingMethodReadAsAValueMovesTheReceiver",
			src: `
				class C {
					v: number,
					take(self) -> number { return self.v },
				}
				fn go() -> number {
					val c = C(1)
					val g = c.take
					g()
					return c.v
				}
			`,
			want: []string{"10:13-10:16: use of moved value 'c'"},
		},
		{
			// The program of B8. c copies the borrow a holds, so c reaches x too.
			name: "CopiedBorrowConflictsAtTheCall",
			src: `
				declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined
				fn g() {
					val mut x = {v: 1}
					val a = &x
					val c = a
					val b = &mut x
					readWrite(c, b)
				}
			`,
			want: []string{"7:14-7:20: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		{
			name: "CopiedBorrowWithNoConflictOk",
			src: `
				declare fn read(a: &{v: number}) -> number
				fn g() -> number {
					val mut x = {v: 1}
					val a = &x
					val c = a
					return read(c) + read(a)
				}
			`,
		},
		{
			// a is not read after the copy, and c still is, so the loan stays live.
			name: "CopyKeepsTheLoanAfterTheOriginalHolderDies",
			src: `
				fn g() -> number {
					val mut x = {v: 1}
					val a = &x
					val c = a
					x.v = 2
					return c.v
				}
			`,
			want: []string{"6:6-6:9: cannot assign to 'x.v' while it is borrowed as immutable"},
		},
		{
			// A reassignment copies the borrow the way a binding does.
			name: "ReassignedCopyHoldsTheLoan",
			src: `
				declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined
				fn g() {
					val mut x = {v: 1}
					val mut y = {v: 2}
					val a = &x
					var c = &y
					c = a
					val b = &mut x
					readWrite(c, b)
				}
			`,
			want: []string{"9:14-9:20: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		{
			// `a = a` leaves a holding the loan of x.
			name: "SelfAssignmentKeepsTheLoan",
			src: `
				fn g() {
					val mut x = {v: 1}
					var a: &{v: number} = &x
					a = a
					x.v = 2
					a
				}
			`,
			want: []string{"6:6-6:9: cannot assign to 'x.v' while it is borrowed as immutable"},
		},
		{
			// c takes b's borrows field by field, and writing `c.peer` ends the one it held
			// there, so moving d is accepted.
			name: "RepointingAFieldOfACopyEndsItsLoan",
			src: `
				fn go() {
					val mut d = {value: 1}
					val mut e = {value: 2}
					val b = {peer: &mut d, other: &mut e}
					var mut c = b
					c.peer = &mut e
					val y = d
					val z = c
				}
			`,
		},
		{
			// The store gives `p.peer` the borrow of b, and the body reads p again after the
			// move, so the move breaks E3.
			name: "StoreIntoACallerOwnedTargetReadAfterAMove",
			src: `
				declare fn store<'a>(target: &mut {peer: &'a mut {x: number}}, item: &'a mut {x: number}) -> undefined
				fn go(p: &mut {peer: &mut {x: number}}) -> number {
					val mut b = {x: 1}
					store(p, &mut b)
					val c = b
					return p.peer.x
				}
			`,
			want: []string{"6:14-6:15: cannot move 'b' while it is borrowed"},
		},
		{
			// L1 lets a borrow outlive its owner, so storing `&p` into a caller-owned element is
			// accepted after p's block ends.
			name: "ElementStoreOutlivesItsOwnerOk",
			src: `
				fn go(arr: &mut Array<&{x: number}>, c: boolean) -> number {
					if c {
						val p = {x: 1}
						arr[0] = &p
					}
					return arr[0].x
				}
			`,
		},
		{
			// The element store lends p to arr, and the return reads arr[0] after the write.
			name: "WriteWhileAnElementStoreHoldsTheBorrow",
			src: `
				fn go(arr: &mut Array<&{x: number}>) -> number {
					val mut p = {x: 1}
					arr[0] = &p
					p.x = 2
					return arr[0].x
				}
			`,
			want: []string{"5:6-5:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			// The return copies a number out of r, so it hands the caller no path to p, and the
			// move of p into o.f is the one diagnostic.
			name: "ReturnOfAValueReadThroughABorrowReachesNothing",
			src: `
				fn go(o: &mut {f: {x: number}}) -> number {
					val p = {x: 1}
					val r = &p
					o.f = p
					return r.x
				}
			`,
			want: []string{"5:12-5:13: cannot move 'p' while it is borrowed"},
		},
		{
			// The closure writes p.b and the borrow is of p.a, so the call is accepted.
			name: "ClosureWritingADisjointFieldOk",
			src: `
				fn go() -> number {
					val mut p = {a: {x: 1}, b: {x: 2}}
					val r = &p.a
					val f = fn () { p.b.x = 3 }
					f()
					return r.x
				}
			`,
		},
		{
			// The closure writes p.a while r borrows it, so the call conflicts.
			name: "ClosureWritingTheBorrowedField",
			src: `
				fn go() -> number {
					val mut p = {a: {x: 1}, b: {x: 2}}
					val r = &p.a
					val f = fn () { p.a.x = 3 }
					f()
					return r.x
				}
			`,
			want: []string{"6:6-6:7: this call runs 'f', which writes 'p.a' while it is borrowed as immutable"},
		},
		{
			// Each iteration declares a new q, so the move on the previous iteration does not
			// reach it.
			name: "MoveOfABindingDeclaredInALoopOk",
			src: `
				declare fn take(q: {x: number}) -> undefined
				fn go() {
					for i in [1, 2] {
						val q = {x: 1}
						take(q)
					}
				}
			`,
		},
		{
			// A declaration whose initializer branches declares a new q on each iteration too.
			name: "MoveOfABranchingDeclarationInALoopOk",
			src: `
				declare fn take(q: {x: number}) -> undefined
				fn go(c: boolean) {
					for i in [1, 2] {
						val q = if c { {x: 1} } else { {x: 2} }
						take(q)
					}
				}
			`,
		},
		{
			// q is declared outside the loop, so the next iteration moves it again.
			name: "MoveOfABindingDeclaredOutsideALoop",
			src: `
				declare fn take(q: {x: number}) -> undefined
				fn go() {
					val q = {x: 1}
					for i in [1, 2] {
						take(q)
					}
				}
			`,
			want: []string{"6:12-6:13: use of moved value 'q'"},
		},
		{
			// A literal with no expected type is an owned destination, so T6 rejects a borrow in an
			// element. A written borrow makes its element a borrow, and an annotation gives each
			// element the type of its field.
			name: "BorrowIntoAnUntypedLiteral",
			src: `
				fn go(r: &{x: number}, m: &mut {x: number}) {
					val p = {x: 1}
					val a = {f: r}
					val b = [{g: m}]
					val c = {f: &p}
					val d: {f: &{x: number}} = {f: r}
				}
			`,
			want: []string{
				"4:18-4:19: cannot use borrowed &object as owned object",
				"5:19-5:20: cannot use borrowed &mut object as owned object",
			},
		},
		{
			// Each branch of a conditional element is checked on its own, so a written borrow on
			// each branch is accepted. A literal a written borrow lends is checked too, since its
			// elements are owned destinations.
			name: "BorrowIntoAnUntypedLiteralThroughABranchOrABorrow",
			src: `
				fn go(c: boolean, r: &{x: number}) {
					val p = {x: 1}
					val q = {x: 2}
					val a = {f: if c { &p } else { &q }}
					val b = {f: if c { &p } else { r }}
					val d = {f: &{g: r}}
				}
			`,
			want: []string{
				"6:37-6:38: cannot use borrowed &object as owned object",
				"7:23-7:24: cannot use borrowed &object as owned object",
			},
		},
	}

	for _, tc := range cases {
		for _, dataflow := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dataflow=%t", tc.name, dataflow), func(t *testing.T) {
				res := InferModuleAgainstStdlib(parseModule(t, tc.src), benchTree, Options{NoOwnershipDataflow: !dataflow})
				require.Equal(t, tc.want, messagesWithSpan(t, res.Errors))
			})
		}
	}
}
