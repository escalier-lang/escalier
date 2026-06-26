package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Move / use-after-move tests for PR 6 of the affine-semantics plan. Each flow site
// — a binding, a reassignment, a return, an owned-parameter argument, and a
// module-level write — consumes an owned source, so a later use of it is a
// use-after-move. A borrow site leaves the source usable.

// A `val` binding of an owned value into another owned binding moves it, so the
// later use of the source is a use-after-move. This is the freeze direction: the
// owned-mutable source is moved into an immutable binding and consumed.
func TestMoveValBindingConsumesSource(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn test() {
			val p: mut {x: number} = {x: 0}
			val q = p
			p.x
		}
	`)
	require.Equal(t, []string{"use of moved value 'p'"}, Messages(errs))
}

// Binding through an explicit `&` borrow does NOT move the source: `val q = &p`
// borrows p, so the later read of p is allowed.
func TestMoveBorrowBindingKeepsSource(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn test() {
			val p: mut {x: number} = {x: 0}
			val q = &p
			p.x
		}
	`)
	require.Empty(t, Messages(errs))
}

// A `&` annotation borrows rather than moves, so the source stays usable.
func TestMoveBorrowAnnotationKeepsSource(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn test() {
			val p: mut {x: number} = {x: 0}
			val q: &{x: number} = p
			p.x
		}
	`)
	require.Empty(t, Messages(errs))
}

// Passing an owned value to a bare owned parameter moves it into the callee, so the
// caller's later use is a use-after-move. This is the `storeGlobally` example.
func TestMoveOwnedArgumentConsumed(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn store(p: {x: number}) {}
		fn test() {
			val p = {x: 0}
			store(p)
			p.x
		}
	`)
	require.Equal(t, []string{"use of moved value 'p'"}, Messages(errs))
}

// Passing an owned value to a `&` parameter auto-borrows, so the caller keeps the
// value and its later use is fine.
func TestMoveBorrowParameterKeepsArgument(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn read(p: &{x: number}) {}
		fn test() {
			val p = {x: 0}
			read(p)
			p.x
		}
	`)
	require.Empty(t, Messages(errs))
}

// Returning an owned value moves it out of the frame. A second occurrence of the
// same owned binding in the returned tuple is a use-after-move, the `dup` reuse
// within a single statement.
func TestMoveReturnDuplicateIsUseAfterMove(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn dup(x: {a: number}) -> [{a: number}, {a: number}] {
			return [x, x]
		}
	`)
	require.Equal(t, []string{"use of moved value 'x'"}, Messages(errs))
}

// The `&` form of the duplicate succeeds: a borrow parameter is copied, not moved, so
// the body may return it twice. This is the concrete counterpart to the generic
// `fn dup<T>(x: &T)`; the type-parameter form awaits TypeParam support in the new
// solver (M7), where `fn dup<T>(x: T) -> [T, T]` is the no-Copy rejection case and
// the `&T` form the accepted one.
func TestMoveDupBorrowParameterAccepted(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn dup(x: &{a: number}) -> [&{a: number}, &{a: number}] {
			return [x, x]
		}
	`)
	require.Empty(t, Messages(errs))
}

// A value moved on only one branch of an if/else is a conditional use-after-move at
// a later read: some reaching path moved it.
func TestMoveConditionalUseAfterMove(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn store(p: {x: number}) {}
		fn test(cond: boolean) {
			val p = {x: 0}
			if cond {
				store(p)
			} else {
			}
			p.x
		}
	`)
	require.Equal(t, []string{"use of moved value 'p'"}, Messages(errs))
}

// A value moved on every branch is an unconditional use-after-move at a later read.
func TestMoveBothBranchesUseAfterMove(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn store(p: {x: number}) {}
		fn test(cond: boolean) {
			val p = {x: 0}
			if cond {
				store(p)
			} else {
				store(p)
			}
			p.x
		}
	`)
	require.Equal(t, []string{"use of moved value 'p'"}, Messages(errs))
}

// A move confined to one branch does not consume the source on the path that did
// not move it: a use INSIDE the untouched branch is allowed.
func TestMoveBranchLocalDoesNotLeak(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn store(p: {x: number}) {}
		fn test(cond: boolean) {
			val p = {x: 0}
			if cond {
				store(p)
			} else {
				p.x
			}
		}
	`)
	require.Empty(t, Messages(errs))
}

// Storing an owned value into a field moves it into the receiver, so the source's
// later use is a use-after-move.
func TestMoveFieldStoreConsumesSource(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn test() {
			val obj: mut {f: {x: number}} = {f: {x: 0}}
			val p = {x: 1}
			obj.f = p
			p.x
		}
	`)
	require.Equal(t, []string{"use of moved value 'p'"}, Messages(errs))
}

// PR 6 retires the M4 G3 implicit reborrow: binding a borrowed source into a bare
// owned annotation is now a borrow-into-owned escape, rejected rather than silently
// reborrowed. The explicit `&` form remains the opt-in for an alias.
func TestMoveBorrowedIntoOwnedAnnotationRejected(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: &mut {x: number}) {
			val q: {x: number} = p
			return q
		}
	`)
	require.Equal(t, []string{
		"borrowed value mut object does not live long enough to satisfy object",
	}, Messages(errs))
}
