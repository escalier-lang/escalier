package solver

import (
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
// rejects, a program each rule allows, a move on one branch of a join, and a move in a loop.
// errs lists every diagnostic the program reports, and is nil for a program that checks
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
			errs: []string{"cannot assign to 'xs' while it is borrowed as immutable"},
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
