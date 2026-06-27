package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Move and use-after-move tests for the affine move engine. Each flow site consumes
// an owned source, so a later use of it is a use-after-move. The flow sites are a
// binding, a reassignment, a return, an owned-parameter argument, a field store, an
// object or tuple literal element, and a module-level write. A borrow site leaves the
// source usable. want is nil for the cases that check cleanly.
func TestMoveSemantics(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// Binding an owned value into another owned binding moves it, so the later use of
		// the source is a use-after-move.
		"ValBindingConsumesSource": {
			src: `
				fn test() {
					val p: mut {x: number} = {x: 0}
					val q = p
					p.x
				}
			`,
			want: []string{"use of moved value 'p'"},
		},
		// An explicit `&` borrow does not move the source.
		"BorrowBindingKeepsSource": {
			src: `
				fn test() {
					val p: mut {x: number} = {x: 0}
					val q = &p
					p.x
				}
			`,
		},
		// A `&` annotation borrows rather than moves, so the source stays usable.
		"BorrowAnnotationKeepsSource": {
			src: `
				fn test() {
					val p: mut {x: number} = {x: 0}
					val q: &{x: number} = p
					p.x
				}
			`,
		},
		// Passing an owned value to a bare owned parameter moves it into the callee.
		"OwnedArgumentConsumed": {
			src: `
				fn store(p: {x: number}) {}
				fn test() {
					val p = {x: 0}
					store(p)
					p.x
				}
			`,
			want: []string{"use of moved value 'p'"},
		},
		// Passing an owned value to a `&` parameter auto-borrows and keeps it usable.
		"BorrowParameterKeepsArgument": {
			src: `
				fn read(p: &{x: number}) {}
				fn test() {
					val p = {x: 0}
					read(p)
					p.x
				}
			`,
		},
		// A too-many-arguments call still moves each argument that lines up with a
		// parameter, so the first p in store(p, p) is consumed and the later p.x is a
		// use-after-move. The surplus second argument has no parameter to move into.
		"ArityMismatchConsumesMatchedArgs": {
			src: `
				fn store(p: {x: number}) {}
				fn test() {
					val p = {x: 0}
					store(p, p)
					p.x
				}
			`,
			want: []string{
				"Too many arguments: expected at most 1, but got 2",
				"use of moved value 'p'",
			},
		},
		// Returning an owned value moves it out of the frame. A second occurrence of the
		// same owned binding in the returned tuple is a use-after-move within one
		// statement.
		"ReturnDuplicateIsUseAfterMove": {
			src: `
				fn dup(x: {a: number}) -> [{a: number}, {a: number}] {
					return [x, x]
				}
			`,
			want: []string{"use of moved value 'x'"},
		},
		// A borrow parameter is copied, not moved, so the body may return it twice. This
		// is the concrete counterpart to the generic `fn dup<T>(x: &T)`; the
		// type-parameter form awaits TypeParam support in the new solver.
		"DupBorrowParameterAccepted": {
			src: `
				fn dup(x: &{a: number}) -> [&{a: number}, &{a: number}] {
					return [x, x]
				}
			`,
		},
		// Spreading an owned tuple moves its elements into the new tuple.
		"TupleSpreadConsumesSource": {
			src: `
				fn test() {
					val xs: [{a: number}] = [{a: 1}]
					val ys = [...xs]
					xs
				}
			`,
			want: []string{"use of moved value 'xs'"},
		},
		// A value moved on only one branch is a conditional use-after-move at a later
		// read, since some reaching path moved it.
		"ConditionalUseAfterMove": {
			src: `
				fn store(p: {x: number}) {}
				fn test(cond: boolean) {
					val p = {x: 0}
					if cond {
						store(p)
					} else {
					}
					p.x
				}
			`,
			want: []string{"use of moved value 'p'"},
		},
		// A value moved on every branch is an unconditional use-after-move at a later
		// read.
		"BothBranchesUseAfterMove": {
			src: `
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
			`,
			want: []string{"use of moved value 'p'"},
		},
		// A move confined to one branch does not consume the source on the path that did
		// not move it, so a use inside the untouched branch is allowed.
		"BranchLocalDoesNotLeak": {
			src: `
				fn store(p: {x: number}) {}
				fn test(cond: boolean) {
					val p = {x: 0}
					if cond {
						store(p)
					} else {
						p.x
					}
				}
			`,
		},
		// Storing an owned value into a field moves it into the receiver.
		"FieldStoreConsumesSource": {
			src: `
				fn test() {
					val obj: mut {f: {x: number}} = {f: {x: 0}}
					val p = {x: 1}
					obj.f = p
					p.x
				}
			`,
			want: []string{"use of moved value 'p'"},
		},
		// The move reconciliation is path-sensitive. A consuming move of p on one branch
		// does not suppress the exclusivity check for p on a sibling branch where it was
		// not moved, so the immutable borrow of a still-mutated p is a real Rule 1
		// conflict and is reported.
		"PathSensitiveExclusivityKept": {
			src: `
				fn store(p: {x: number}) {}
				fn test(cond: boolean) {
					val p: mut {x: number} = {x: 0}
					if cond {
						store(p)
					} else {
						val snapshot: &{x: number} = p
						p.x = 5
						snapshot
					}
				}
			`,
			want: []string{
				"cannot assign 'p' to immutable 'snapshot': 'p' is still used mutably after this point",
			},
		},
		// Storing p into the global consumes it, so the later borrow `val snap = p` is a
		// single use-after-move, not also a stale 'static-escape transition.
		"GlobalEscapeReportsSingleUseAfterMove": {
			src: `
				var sink = {x: 0}
				fn cache(p: &mut {x: number}) {
					sink = p
					val snap: &{x: number} = p
					snap
				}
			`,
			want: []string{"use of moved value 'p'"},
		},
		// Moving a value while a mutable borrow of it is live is rejected: freezing p
		// into immutable q while r still holds a mutable borrow would let r mutate a
		// value q reads as immutable.
		"MoveWithLiveBorrowRejected": {
			src: `
				fn test() {
					val p: mut {x: number} = {x: 0}
					val r: &mut {x: number} = p
					val q: {x: number} = p
					r.x = 5
					q.x
				}
			`,
			want: []string{
				"cannot assign 'p' to immutable 'q': 'r' still has mutable access to 'p' after this point",
			},
		},
		// Binding a borrowed source into a bare owned annotation is a borrow-into-owned
		// escape, rejected rather than silently reborrowed. The explicit `&` form remains
		// the opt-in for an alias.
		"BorrowedIntoOwnedAnnotationRejected": {
			src: `
				fn f(p: &mut {x: number}) {
					val q: {x: number} = p
					return q
				}
			`,
			want: []string{
				"borrowed value mut object does not live long enough to satisfy object",
			},
		},
		// Moving one field out of an owned object consumes only that field's slot. The
		// sibling stays usable and a later read of the moved field is a use-after-move
		// naming the field place (PR 7).
		"PartialMoveConsumesFieldKeepsSibling": {
			src: `
				fn store(p: {id: number}) {}
				fn test() {
					val pair = {a: {id: 1}, b: {id: 2}}
					store(pair.a)
					pair.b.id
					pair.a.id
				}
			`,
			want: []string{"use of moved value 'pair.a'"},
		},
		// Reading a sibling field after a partial move is allowed on its own.
		"SiblingAfterPartialMoveAccepted": {
			src: `
				fn store(p: {id: number}) {}
				fn test() {
					val pair = {a: {id: 1}, b: {id: 2}}
					store(pair.a)
					pair.b.id
				}
			`,
		},
		// A read of the whole object after a partial move exposes the moved field, so it
		// is a use-after-move even though a sibling is still live.
		"WholeObjectReadAfterPartialMove": {
			src: `
				fn store(p: {id: number}) {}
				fn test() {
					val pair = {a: {id: 1}, b: {id: 2}}
					store(pair.a)
					pair
				}
			`,
			want: []string{"use of partially moved value 'pair'; field 'pair.a' was moved out"},
		},
		// Binding a field into an owned binding moves that field; a later read of it is a
		// use-after-move.
		"PartialMoveViaBinding": {
			src: `
				fn test() {
					val pair = {a: {id: 1}, b: {id: 2}}
					val q = pair.a
					pair.a.id
				}
			`,
			want: []string{"use of moved value 'pair.a'"},
		},
		// Storing a field into a longer-lived object moves it; the sibling stays usable.
		"PartialMoveViaFieldStore": {
			src: `
				fn test() {
					val pair = {a: {id: 1}, b: {id: 2}}
					val obj: mut {f: {id: number}} = {f: {id: 0}}
					obj.f = pair.a
					pair.b.id
					pair.a.id
				}
			`,
			want: []string{"use of moved value 'pair.a'"},
		},
		// A field built into a tuple literal moves as a partial move, the gap PR 6 left
		// for PR 7 to close; the sibling stays usable.
		"PartialMoveIntoLiteral": {
			src: `
				fn test() {
					val pair = {a: {id: 1}, b: {id: 2}}
					val ys = [pair.a]
					pair.b.id
					pair.a.id
				}
			`,
			want: []string{"use of moved value 'pair.a'"},
		},
		// A field moved on only one branch is a conditional use-after-move at a later
		// read, joined to MaybeMoved at the branch merge.
		"ConditionalPartialMove": {
			src: `
				fn store(p: {id: number}) {}
				fn test(cond: boolean) {
					val pair = {a: {id: 1}, b: {id: 2}}
					if cond {
						store(pair.a)
					} else {
					}
					pair.a.id
				}
			`,
			want: []string{"use of moved value 'pair.a'"},
		},
		// Tracking reaches nested field paths. Moving `pair.a.inner` consumes only that
		// deep slot, so the sibling `pair.a.keep` stays usable and a read of the moved
		// slot is a use-after-move naming the full path.
		"NestedFieldPartialMove": {
			src: `
				fn store(p: {id: number}) {}
				fn test() {
					val pair = {a: {inner: {id: 1}, keep: {id: 2}}, b: {id: 3}}
					store(pair.a.inner)
					pair.a.keep.id
					pair.a.inner.id
				}
			`,
			want: []string{"use of moved value 'pair.a.inner'"},
		},
		// A field whose key is not a valid identifier is reached by constant-string
		// index and renders in bracket notation, so the moved place reads back as
		// `pair["a.b"]` rather than collapsing into the `pair.a.b` nested access.
		"BracketKeyPartialMove": {
			src: `
				fn store(p: {x: number}) {}
				fn test() {
					val pair = {"a.b": {x: 1}, c: {x: 2}}
					store(pair["a.b"])
					pair["a.b"].x
				}
			`,
			want: []string{`use of moved value 'pair["a.b"]'`},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, Messages(errs))
		})
	}
}
