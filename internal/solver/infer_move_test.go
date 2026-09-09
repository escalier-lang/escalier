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
			want: []string{"5:6-5:9: use of moved value 'p'"},
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
			want: []string{"6:6-6:9: use of moved value 'p'"},
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
				"5:6-5:17: Too many arguments: expected at most 1, but got 2",
				"6:6-6:9: use of moved value 'p'",
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
			want: []string{"3:17-3:18: use of moved value 'x'"},
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
			want: []string{"5:6-5:8: use of moved value 'xs'"},
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
			want: []string{"9:6-9:9: use of moved value 'p'"},
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
			want: []string{"10:6-10:9: use of moved value 'p'"},
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
			want: []string{"6:6-6:9: use of moved value 'p'"},
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
				"8:7-8:37: cannot assign 'p' to immutable 'snapshot': 'p' is still used mutably after this point",
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
			want: []string{"5:31-5:32: use of moved value 'p'"},
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
				"5:6-5:28: cannot assign 'p' to immutable 'q': 'r' still has mutable access to 'p' after this point",
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
				"2:13-2:29: borrowed value mut object does not live long enough to satisfy object",
			},
		},
		// Moving one field out of an owned object consumes only that field. The
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
			want: []string{"7:6-7:15: use of moved value 'pair.a'"},
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
			want: []string{"6:6-6:10: use of partially moved value 'pair'; field 'pair.a' was moved out"},
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
			want: []string{"5:6-5:15: use of moved value 'pair.a'"},
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
			want: []string{"7:6-7:15: use of moved value 'pair.a'"},
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
			want: []string{"6:6-6:15: use of moved value 'pair.a'"},
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
			want: []string{"9:6-9:15: use of moved value 'pair.a'"},
		},
		// Tracking reaches nested field paths. Moving `pair.a.inner` consumes only that
		// deep field, so the sibling `pair.a.keep` stays usable and a read of the moved
		// field is a use-after-move naming the full path.
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
			want: []string{"7:6-7:21: use of moved value 'pair.a.inner'"},
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
			want: []string{`6:6-6:19: use of moved value 'pair["a.b"]'`},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

// TestThawMove covers the immutable→mutable thaw. `val mut q = p` for an
// owned-immutable `p` moves `p` into the mutable binding `q` and consumes it. The move
// leaves `q` the sole owner, so `q` may be mutable, and `q.x = 5` is accepted with no
// immutable-object error. The later `p.x` reads `p` after it was moved. That read is a
// use-after-move, and it is the only diagnostic. This is the requirements' thawing
// example.
func TestThawMove(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn test() {
			val p = {x: 0}
			val mut q = p
			q.x = 5
			p.x
		}
	`)
	require.Equal(t, []string{"6:4-6:7: use of moved value 'p'"}, messagesWithSpan(t, errs))
}

// TestFreezeMove covers the mutable→immutable freeze. A plain `val q = p` for an
// owned-mutable `p` moves `p` into the immutable binding `q` and consumes it. The
// binding's mutability comes from the pattern, so `q` is immutable and the write
// `q.x = 5` is rejected. The later `p.x` is a use-after-move on the consumed source.
// Both diagnostics stand. This is the plan's freeze example.
func TestFreezeMove(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn test() {
			val mut p = {x: 0}
			p.x = 42
			val q = p
			q.x = 5
			p.x
		}
	`)
	require.Equal(t, []string{
		"6:4-6:11: cannot constrain immutable object <: mutable object",
		"7:4-7:7: use of moved value 'p'",
	}, messagesWithSpan(t, errs))
}

// TestFreezeMoveAllowed shows the freeze move itself is allowed. Binding an
// owned-mutable `p` into a plain `val q` moves the value into an immutable owner and
// consumes `p`. With `p` never used again, the move produces no error. `q` is
// owned-immutable, so the function returns the value at the frozen type
// `fn (p: mut {x: number}) -> {x: number}`. This is the mirror of
// TestInferValMutThawFromVariable, which thaws an owned-immutable source into an
// owned-mutable binding.
func TestFreezeMoveAllowed(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: mut {x: number}) {
  val q = p
  return q
}`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {x: number}) -> {x: number}", values["f"])
}

// An argument a rest slot gathers is carried out of the frame the way one passed to a bare
// owned parameter is, so every argument the slot absorbs is consumed and not just the first.
// The gathered array is what the callee owns, so the slot's element type is what says whether
// an argument moves.
func TestRestParamConsumesEveryArgument(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// The gap this case pins. Where only a fixed position consumes its argument, both
			// reads below stand and a value the callee owns stays readable here.
			name: "SecondGatheredArgumentIsMoved",
			src: `fn store(...xs: Array<{x: number}>) -> number { return 1 }
fn f() {
  val p = {x: 1}
  val q = {x: 2}
  store(p, q)
  q.x
}`,
			want: []string{"6:3-6:6: use of moved value 'q'"},
		},
		{
			name: "FirstGatheredArgumentIsMoved",
			src: `fn store(...xs: Array<{x: number}>) -> number { return 1 }
fn f() {
  val p = {x: 1}
  val q = {x: 2}
  store(p, q)
  p.x
}`,
			want: []string{"6:3-6:6: use of moved value 'p'"},
		},
		{
			// A primitive element is not a concrete owned shape, so the slot moves nothing
			// and a later read stands. That is the same conservative reading a fixed
			// position of a primitive type takes.
			name: "APrimitiveElementMovesNothing",
			src: `fn store(...xs: Array<number>) -> number { return 1 }
fn f() -> number {
  val p = 1
  store(p, 2)
  return p
}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			if len(tt.want) == 0 {
				require.Empty(t, errs)
				return
			}
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

// A call resolved through an overload set moves the arguments the winning arm consumes, the
// way a call to a single signature does. Resolution owns the argument checking and leaves
// inferCall before its move recording runs, so the arm it picks has to drive that separately.
// All three overload callees take the same path: a named set, a method, and a constructor.
func TestOverloadCallConsumesArguments(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "NamedOverloadSet",
			src: `fn take(a: {x: number}) -> number { return 1 }
fn take(a: {x: number}, b: number) -> number { return 2 }
fn f() {
  val p = {x: 1}
  take(p)
  p.x
}`,
			want: []string{"6:3-6:6: use of moved value 'p'"},
		},
		{
			name: "OverloadedMethod",
			src: `declare class Box {
  m(self, a: {x: number}) -> number,
  m(self, a: {x: number}, b: number) -> number,
}
fn f(b: Box) {
  val p = {x: 1}
  b.m(p)
  p.x
}`,
			want: []string{"8:3-8:6: use of moved value 'p'"},
		},
		{
			name: "OverloadedConstructor",
			src: `declare class Box {
  constructor(mut self, a: {x: number}),
  constructor(mut self, a: {x: number}, b: number),
}
fn f() {
  val p = {x: 1}
  Box(p)
  p.x
}`,
			want: []string{"8:3-8:6: use of moved value 'p'"},
		},
		{
			// No arm accepted the call, so no signature says which arguments it would have
			// consumed. The no-match is the only diagnostic; the later read still stands.
			name: "NoMatchingArmMovesNothing",
			src: `declare class Box {
  constructor(mut self, a: number),
  constructor(mut self, a: number, b: number),
}
fn f() {
  val p = {x: 1}
  Box(p)
  p.x
}`,
			want: []string{"7:3-7:9: No matching overload for this call\n  fn (a: number) -> Box\n  fn (a: number, b: number) -> Box"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}
