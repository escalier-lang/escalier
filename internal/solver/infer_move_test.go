package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// Move and use-after-move tests. Each flow site moves an owned source, so a later use of
// it is a use-after-move. The flow sites are a binding, a reassignment, a return, an
// owned-parameter argument, a field store, an object or tuple literal element, and a
// module-level write. A borrow site leaves the source usable. want is nil for the cases
// that check cleanly.
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
					val mut p: {x: number} = {x: 0}
					val q = p
					p.x
				}
			`,
			want: []string{"5:6-5:9: use of moved value 'p'"},
		},
		// An optional link reads the place the plain link names, so `p?.x` reads `p.x`.
		"OptionalLinkReadsAMovedBinding": {
			src: `
				fn test() {
					val mut p: {x: number} = {x: 0}
					val q = p
					p?.x
				}
			`,
			want: []string{"5:6-5:10: use of moved value 'p'"},
		},
		"OptionalLinkReadsAMovedField": {
			src: `
				fn test() {
					val mut b = {v: {x: 1}}
					val n = b.v
					b.v?.x
				}
			`,
			want: []string{"5:6-5:12: use of moved value 'b.v'"},
		},
		"OptionalLinkMovesAnObjectField": {
			src: `
				fn test() {
					val mut b = {v: {x: 1}}
					val n = b?.v
					val m = b?.v
				}
			`,
			want: []string{"5:14-5:18: use of moved value 'b.v'"},
		},
		"OptionalLinkAfterASiblingMoveOk": {
			src: `
				fn test() {
					val mut b = {v: {x: 1}, w: {x: 2}}
					val n = b.v
					b?.w.x
				}
			`,
		},
		// An alias that names a borrow is a borrow, so binding it copies the borrow and the
		// second binding reads a source that was not moved.
		"AliasOfBorrowBoundTwiceOk": {
			src: `
				type R = &{x: number}
				fn test(r: R) {
					val a = r
					val b = r
				}
			`,
		},
		// A field of value type copies, so binding it moves nothing and the field and its
		// object stay usable.
		"ValueFieldReadTwiceOk": {
			src: `
				fn test() {
					val mut b = {v: 1}
					val n = b.v
					val m = b.v
				}
			`,
		},
		"ValueFieldReadThenWholeObjectOk": {
			src: `
				fn test() -> {v: number} {
					val mut b = {v: 1}
					val n = b.v
					return b
				}
			`,
		},
		"ValueFieldOfAnnotatedBindingOk": {
			src: `
				fn test() {
					val mut b: {v: number} = {v: 1}
					val n = b.v
					val m = b.v
				}
			`,
		},
		"ValueFieldOfAParameterOk": {
			src: `
				fn test(mut b: {v: number}) -> undefined {
					val n = b.v
					val m = b.v
				}
			`,
		},
		// An object-typed field is owned, so binding it moves it out of its object.
		"ObjectFieldReadTwiceMoves": {
			src: `
				fn test() {
					val mut b = {v: {x: 1}}
					val n = b.v
					val m = b.v
				}
			`,
			want: []string{"5:14-5:17: use of moved value 'b.v'"},
		},
		// A field typed by a type parameter may hold an owned object, so binding it moves it.
		"TypeParameterFieldMoves": {
			src: `
				fn test<T>(b: {v: T}) -> [T, T] {
					val n = b.v
					return [n, b.v]
				}
			`,
			want: []string{"4:17-4:20: use of moved value 'b.v'"},
		},
		// An explicit `&` borrow does not move the source.
		"BorrowBindingKeepsSource": {
			src: `
				fn test() {
					val mut p: {x: number} = {x: 0}
					val q = &p
					p.x
				}
			`,
		},
		// A binding annotated as a `&` borrow takes `&p` without moving p, so the source stays
		// usable.
		"BorrowAnnotationKeepsSource": {
			src: `
				fn test() {
					val mut p: {x: number} = {x: 0}
					val q: &{x: number} = &p
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
		// A `&` parameter borrows its argument rather than consuming it, so the argument stays
		// usable afterwards. The borrow is written at the call, which #1541 requires.
		"BorrowParameterKeepsArgument": {
			src: `
				fn read(p: &{x: number}) {}
				fn test() {
					val p = {x: 0}
					read(&p)
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
		// Destructuring moves each part it binds by value, the partial move a field read
		// makes. b.x moves into x, so reading b whole afterwards reads a moved field.
		"DestructuringMovesTheBoundField": {
			src: `
				fn f() {
					val b = {x: {v: 1}, y: {v: 2}}
					val {x} = b
					val z = b
				}
			`,
			want: []string{"5:14-5:15: use of partially moved value 'b'; field 'b.x' was moved out"},
		},
		// A field the pattern does not bind stays with b.
		"DestructuringLeavesAnUnboundFieldUsable": {
			src: `
				fn f() {
					val b = {x: {v: 1}, y: {v: 2}}
					val {x} = b
					val z = b.y
				}
			`,
		},
		// An object rest element moves the fields the pattern does not name.
		"DestructuringARestElementMovesTheRemainingFields": {
			src: `
				fn f() {
					val b = {x: {v: 1}, y: {v: 2}}
					val {x, ...rest} = b
					val z = b.y
				}
			`,
			want: []string{"5:14-5:17: use of moved value 'b.y'"},
		},
		// A rest element moves every field the pattern does not name, not only the first.
		"DestructuringARestElementMovesEachRemainingField": {
			src: `
				fn f() {
					val b = {x: {v: 1}, y: {v: 2}, z: {v: 3}}
					val {x, ...rest} = b
					val n = b.z
				}
			`,
			want: []string{"5:14-5:17: use of moved value 'b.z'"},
		},
		// A tuple rest element moves the tuple, as every tuple leaf does.
		"DestructuringATupleRestMovesTheTuple": {
			src: `
				fn f() {
					val b = [{v: 1}, {v: 2}]
					val [x, ...rest] = b
					val z = b
				}
			`,
			want: []string{"5:14-5:15: use of moved value 'b'"},
		},
		// A tuple element has no field segment, so two leaves of one tuple move the tuple once.
		"DestructuringATupleMovesItOnce": {
			src: `
				fn f() {
					val b = [{v: 1}, {v: 2}]
					val [x, y] = b
					val z = b
				}
			`,
			want: []string{"5:14-5:15: use of moved value 'b'"},
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
					val mut obj: {f: {x: number}} = {f: {x: 0}}
					val p = {x: 1}
					obj.f = p
					p.x
				}
			`,
			want: []string{"6:6-6:9: use of moved value 'p'"},
		},
		// The move reconciliation is path-sensitive. A consuming move of `p` on one branch
		// does not suppress the E2 check for `p` on a sibling branch where it was
		// not moved, so the write to p while snapshot still borrows it is reported.
		"PathSensitiveExclusivityKept": {
			src: `
				fn store(p: {x: number}) {}
				fn test(cond: boolean) {
					val mut p: {x: number} = {x: 0}
					if cond {
						store(p)
					} else {
						val snapshot: &{x: number} = &p
						p.x = 5
						snapshot
					}
				}
			`,
			want: []string{
				"9:7-9:10: cannot assign to 'p.x' while it is borrowed as immutable",
			},
		},
		// A reassignment whose right-hand side branches gives p a new value, so the use after it
		// reads that value, the same as after a plain reassignment.
		"BranchingReassignmentAfterAMove": {
			src: `
				declare fn take(p: {x: number}) -> undefined
				fn go(c: boolean) -> number {
					var p = {x: 1}
					take(p)
					p = if c { {x: 2} } else { {x: 3} }
					return p.x
				}
			`,
		},
		// F2 copies the borrow p into the global, as D8 decides, and the copy is `'static`.
		// The global keeps an immutable path to p for the rest of the program, so the write
		// through p breaks E2.
		"GlobalEscapeReportsStaticImmutablePath": {
			src: `
				var sink = &{x: 0}
				fn cache(p: &mut {x: number}) {
					sink = p
					p.x = 1
				}
			`,
			want: []string{"5:6-5:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		// Moving a value while a mutable borrow of it is live is rejected: freezing p
		// into immutable q while r still holds a mutable borrow would let r mutate a
		// value q reads as immutable.
		"MoveWithLiveBorrowRejected": {
			src: `
				fn test() {
					val mut p: {x: number} = {x: 0}
					val r: &mut {x: number} = &mut p
					val q: {x: number} = p
					r.x = 5
					q.x
				}
			`,
			want: []string{
				"5:27-5:28: cannot move 'p' while it is borrowed",
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
				"2:13-2:29: cannot use borrowed &mut object as owned object",
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
					val mut obj: {f: {id: number}} = {f: {id: 0}}
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
		"6:4-6:11: cannot mutate 'q' because its binding is immutable; declare it as `val mut q`",
		"7:4-7:7: use of moved value 'p'",
	}, messagesWithSpan(t, errs))
}

// TestFreezeMoveAllowed shows the freeze move itself is allowed. Binding an
// owned-mutable `p` into a plain `val q` moves the value into an immutable owner and
// consumes `p`. With `p` never used again, the move produces no error. `q` is
// owned-immutable, so the function returns the value at the frozen type
// `fn (p: {x: number}) -> {x: number}`. This is the mirror of
// TestInferValMutThawFromVariable, which thaws an owned-immutable source into an
// owned-mutable binding.
func TestFreezeMoveAllowed(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(mut p: {x: number}) {
  val q = p
  return q
}`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number}) -> {x: number}", values["f"])
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
			// The gap this case asserts. Where only a fixed position consumes its argument, both
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

// TestUnannotatedMovedParamConsumesArgument covers calls to a parameter written with no type.
// A call moves its argument there when the callee's body moves the whole parameter, and leaves
// the argument usable otherwise.
func TestUnannotatedMovedParamConsumesArgument(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "StoreIntoModuleLevelState",
			src: `var sink = {x: 0}
fn store(p) { sink = p }
fn f() -> number {
  val a = {x: 1}
  store(a)
  return a.x
}`,
			want: []string{"6:10-6:13: use of moved value 'a'"},
		},
		{
			name: "ReturnTheParameter",
			src: `fn give(p) { return p }
fn f() -> number {
  val a = {x: 1}
  val b = give(a)
  return a.x + b.x
}`,
			want: []string{"5:10-5:13: use of moved value 'a'"},
		},
		{
			// relay moves p into store's parameter, so relay's parameter is moved too.
			name: "PassToAnotherMovedParameter",
			src: `var sink = {x: 0}
fn store(p) { sink = p }
fn relay(p) { store(p) }
fn f() -> number {
  val a = {x: 1}
  relay(a)
  return a.x
}`,
			want: []string{"7:10-7:13: use of moved value 'a'"},
		},
		{
			// The second parameter is moved and the first is only read, so the first is a borrow
			// and the call moves b alone.
			name: "OnlyTheMovedPositionConsumes",
			src: `var sink = {x: 0}
fn pick(p, q) -> number {
  sink = q
  return p.x
}
fn f() -> number {
  val a = {x: 1}
  val b = {x: 2}
  pick(&a, b)
  return a.x + b.x
}`,
			want: []string{"10:16-10:19: use of moved value 'b'"},
		},
		{
			// read only reads a field of p, so p is a borrow and the argument stays usable.
			name: "ReadOnlyParameterKeepsArgument",
			src: `fn read(p) { return p.x }
fn f() -> number {
  val a = {x: 1}
  val n = read(&a)
  return a.x + n
}`,
		},
		{
			// A number argument copies into the parameter, so moving the parameter moves nothing.
			name: "ValueTypeArgumentCopies",
			src: `var total = 0
fn keep(p) { total = p }
fn f() -> number {
  val n = 1
  keep(n)
  return n
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

// TestTypeParamConsumesArgument covers calls to a parameter typed by one of the callee's own
// type parameters, either bare or as a union member. An owned argument moves there. The
// caller cannot keep reading it while a mutable binding of the result writes through it. A
// borrow argument and a value-type argument move nothing. The cases run against the stdlib
// so `xs.length` and `r.push` resolve.
func TestTypeParamConsumesArgument(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "WrittenTypeParameter",
			src: `fn id<T>(t: T) -> T { return t }
fn g() -> number {
	val xs: Array<number> = [1]
	val mut r = id(xs)
	r.push(2)
	return xs.length
}`,
			want: []string{"6:9-6:18: use of moved value 'xs'"},
		},
		{
			name: "InferredTypeParameter",
			src: `fn id(x) { return x }
fn g() -> number {
	val xs: Array<number> = [1]
	val mut r = id(xs)
	r.push(2)
	return xs.length
}`,
			want: []string{"6:9-6:18: use of moved value 'xs'"},
		},
		{
			name: "AwaitedAsyncTypeParameter",
			src: `async fn idA<T>(t: T) -> Promise<T> { return t }
async fn g() -> Promise<number> {
	val xs: Array<number> = [1]
	val mut r = await idA(xs)
	r.push(2)
	return xs.length
}`,
			want: []string{"6:9-6:18: use of moved value 'xs'"},
		},
		{
			name: "GenericMethod",
			src: `val m = {id: fn <T>(x: T) -> T { return x }}
fn g() -> number {
	val xs: Array<number> = [1]
	m.id(xs)
	return xs.length
}`,
			want: []string{"5:9-5:18: use of moved value 'xs'"},
		},
		{
			// A union with a type-parameter member takes ownership as the bare parameter does.
			name: "UnionWithTypeParameter",
			src: `fn orNull<T>(t: T | null) -> T | null { return t }
fn g() -> number {
	val xs: Array<number> = [1]
	orNull(xs)
	return xs.length
}`,
			want: []string{"5:9-5:18: use of moved value 'xs'"},
		},
		{
			// Promise.resolve takes `T | PromiseLike<T>` and resolves to the value it was given.
			name: "PromiseResolve",
			src: `fn g() -> number {
	val xs: Array<number> = [1]
	val p = Promise.resolve(xs)
	return xs.length
}`,
			want: []string{"4:9-4:18: use of moved value 'xs'"},
		},
		{
			name: "GenericOverloadArm",
			src: `fn pick<T>(a: T) -> T { return a }
fn pick<T>(a: T, b: number) -> T { return a }
fn g() -> number {
	val xs: Array<number> = [1]
	pick(xs)
	return xs.length
}`,
			want: []string{"6:9-6:18: use of moved value 'xs'"},
		},
		{
			// Every argument a rest parameter typed by a type parameter receives moves.
			// `count(xs)` passes the whole array as one argument, not its elements. T is
			// inferred as `Array<number>`, so `items` is `Array<Array<number>>` and holds
			// xs itself. A non-generic `...items: Array<number>` would reject the call.
			name: "RestTypeParameter",
			src: `fn count<T>(...items: Array<T>) -> number { return items.length }
fn g() -> number {
	val xs: Array<number> = [1]
	count(xs)
	return xs.length
}`,
			want: []string{"5:9-5:18: use of moved value 'xs'"},
		},
		{
			name: "BorrowArgument",
			src: `fn id<T>(t: T) -> T { return t }
fn g() -> number {
	val xs: Array<number> = [1]
	val r = id(&xs)
	return xs.length + r.length
}`,
		},
		{
			// A number, a string, and a function copy into the parameter.
			name: "ValueTypeArguments",
			src: `fn id<T>(t: T) -> T { return t }
fn g() -> number {
	val n = 1
	val s = "a"
	val f = fn () { return 1 }
	id(n)
	id(s)
	id(f)
	return n + s.length + f()
}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
		})
	}
}

// TestInferUnannotatedDestructuredParam covers the signature a function infers for a parameter
// written as a destructuring pattern with no type. A pattern whose leaves the body only returns
// or reads through infers as a borrow, the same signature a body that reads each field off a
// named parameter infers. A leaf that is stored or captured by a returned closure keeps the
// parameter owned.
//
// sig is the callee's rendered signature, and moved lists the positions of the parameters its
// body moves. The rendered signature does not show FuncParam.Moved, which decides whether a
// call moves its argument into an owned parameter, so both are needed.
func TestInferUnannotatedDestructuredParam(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		callee string
		sig    string
		moved  []int
	}{
		{
			name:   "ObjectPatternLeafReturned",
			callee: "take",
			src:    `fn take({x}) { return x }`,
			sig:    "fn <T0, 'a>({x}: &'a {x: T0}) -> &'a T0",
		},
		{
			name:   "FieldReturned",
			callee: "take2",
			src:    `fn take2(p) { return p.x }`,
			sig:    "fn <T0, 'a>(p: &'a {x: T0}) -> &'a T0",
		},
		{
			name:   "BoundFieldReturned",
			callee: "take3",
			src: `fn take3(p) {
	val x = p.x
	return x
}`,
			sig: "fn <T0, 'a>(p: &'a {x: T0}) -> &'a T0",
		},
		{
			name:   "TuplePatternLeafReturned",
			callee: "first",
			src:    `fn first([x, y]) { return x }`,
			sig:    "fn <T0, 'a>([x, y]: &'a [T0, unknown]) -> &'a T0",
		},
		{
			name:   "BareNameReturned",
			callee: "whole",
			src:    `fn whole(p) { return p }`,
			sig:    "fn <T0>(p: T0) -> T0",
			moved:  []int{0},
		},
		{
			name:   "NestedPatternLeafReturned",
			callee: "deep",
			src:    `fn deep({a: {b}}) { return b }`,
			sig:    "fn <T0, 'a>({a: {b}}: &'a {a: {b: T0}}) -> &'a T0",
		},
		{
			name:   "TupleRestReturned",
			callee: "tail",
			src:    `fn tail([x, ...rest]) { return rest }`,
			sig:    "fn <'a>([x, ...rest]: &'a [unknown, ...]) -> &'a [...]",
		},
		{
			name:   "ObjectRestReturned",
			callee: "others",
			src:    `fn others({x, ...rest}) { return rest }`,
			sig:    "fn <'a>({x, ...rest}: &'a {x: unknown, ...}) -> &'a {...}",
		},
		{
			name:   "LeafOnlyRead",
			callee: "read",
			src:    `fn read({x}) -> number { return x.v }`,
			sig:    "fn ({x}: &{x: {v: number}}) -> number",
		},
		{
			name:   "FieldOnlyRead",
			callee: "readWhole",
			src:    `fn readWhole(p) -> number { return p.v }`,
			sig:    "fn (p: &{v: number}) -> number",
		},
		{
			// A `number` leaf copies out of the parameter, so storing it moves nothing.
			name:   "ValueTypeLeafStored",
			callee: "keep",
			src: `var total = 0
fn keep({n}) { total = n }`,
			sig: "fn ({n}: {n: number}) -> undefined",
		},
		{
			// A leaf bounded above by `number | undefined` holds only primitives, so binding it
			// to an annotated local copies it.
			name:   "OptionalNumberLeafCopied",
			callee: "f",
			src: `fn f({n}) {
  val m: number | undefined = n
  return n
}`,
			sig: "fn <T0: number | undefined>({n}: {n: T0}) -> T0",
		},
		{
			// A returned closure that captures a `number` leaf copies it.
			name:   "ReturnedClosureCapturesValueTypeLeaf",
			callee: "f",
			src: `fn f({n}) {
  val m: number = n
  return fn () { return n }
}`,
			sig: "fn <T0: number>({n}: {n: T0}) -> fn () -> T0",
		},
		{
			// A returned closure that captures an object leaf takes it out of the parameter.
			name:   "ReturnedClosureCapturesObjectLeaf",
			callee: "f",
			src: `fn f({x}) {
  return fn () { return x }
}`,
			sig:   "fn <T0>({x}: {x: T0}) -> fn () -> T0",
			moved: []int{0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, messagesWithSpan(t, errs))
			require.Equal(t, tt.sig, values[tt.callee])
			require.Equal(t, tt.moved, inferMovedParams(t, tt.src)[tt.callee])
		})
	}
}

// TestCallToInferredDestructuredParam covers calls to a callee whose destructuring parameter
// has no written type, so the call reads the ownership and the Moved mark the callee infers.
func TestCallToInferredDestructuredParam(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "BorrowedPatternLends",
			src: `fn take({x}) { return x }
fn h() {
  val o = {x: {v: 1}}
  val r = take(&o)
  return o.x.v + r.v
}`,
		},
		{
			name: "BorrowedPatternNeedsWrittenBorrow",
			src: `fn take({x}) { return x }
fn h() {
  val o = {x: {v: 1}}
  val r = take(o)
  val s = o
}`,
			want: []string{"4:16-4:17: this argument is borrowed by the callee, so write the borrow: `&`"},
		},
		{
			name: "ReturnedLeafHoldsBorrow",
			src: `fn take({x}) { return x }
fn h() {
  val o = {x: {v: 1}}
  val r = take(&o)
  val s = o
  return r.v
}`,
			want: []string{"5:11-5:12: cannot move 'o' while it is borrowed"},
		},
		{
			name: "CapturedObjectLeafMoves",
			src: `fn f({x}) {
  return fn () { return x }
}
fn h() {
  val o = {x: {v: 1}}
  val r = f(o)
  val s = o
}`,
			want: []string{"7:11-7:12: use of moved value 'o'"},
		},
		{
			name: "StoredValueTypeLeafKeepsArgument",
			src: `var total = 0
fn keep({n}) { total = n }
fn h() {
  val o = {n: 1}
  keep(o)
  val s = o
}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
		})
	}
}

// TestCallToDestructuredParamSignature covers calls to a callee whose parameter type is written
// out, in the shapes TestInferUnannotatedDestructuredParam infers. A call moves its argument
// into an owned parameter. It lends the argument to a borrowed parameter through a written `&`,
// and reports the missing `&` when the call omits it.
func TestCallToDestructuredParamSignature(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "OwnedObjectMoves",
			src: `declare fn take(p: {x: {v: number}}) -> {v: number}
fn h() {
  val o = {x: {v: 1}}
  val r = take(o)
  val s = o
}`,
			want: []string{"5:11-5:12: use of moved value 'o'"},
		},
		{
			name: "BorrowedObjectLends",
			src: `declare fn read(p: &{x: {v: number}}) -> number
fn h() {
  val o = {x: {v: 1}}
  val n = read(&o)
  val s = o
}`,
		},
		{
			name: "BorrowedObjectNeedsWrittenBorrow",
			src: `declare fn read(p: &{x: {v: number}}) -> number
fn h() {
  val o = {x: {v: 1}}
  val n = read(o)
  val s = o
}`,
			want: []string{"4:16-4:17: this argument is borrowed by the callee, so write the borrow: `&`"},
		},
		{
			name: "OwnedTupleMoves",
			src: `declare fn first(t: [{v: number}, {v: number}]) -> {v: number}
fn h() {
  val t = [{v: 1}, {v: 2}]
  val u = first(t)
  val w = t
}`,
			want: []string{"5:11-5:12: use of moved value 't'"},
		},
		{
			name: "BorrowedTupleLends",
			src: `declare fn first<'a>(t: &'a [{v: number}, {v: number}]) -> &'a {v: number}
fn h() {
  val t = [{v: 1}, {v: 2}]
  val u = first(&t)
  val w = t
}`,
		},
		{
			name: "BorrowedTupleNeedsWrittenBorrow",
			src: `declare fn first<'a>(t: &'a [{v: number}, {v: number}]) -> &'a {v: number}
fn h() {
  val t = [{v: 1}, {v: 2}]
  val u = first(t)
  val w = t
}`,
			want: []string{"4:17-4:18: this argument is borrowed by the callee, so write the borrow: `&`"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
		})
	}
}

// inferMovedParams infers src and returns, for each function the module binds, the positions of
// the parameters whose FuncParam.Moved is set. A function with no moved parameter maps to nil.
func inferMovedParams(t *testing.T, src string) map[string][]int {
	t.Helper()
	module := parseModuleFiles(t, map[string]string{"input.esc": src})
	c := newTestChecker()
	scope := c.preludeScope().Child()
	c.inferDepGraph(scope, 0, module, dep_graph.BuildDepGraph(module))

	moved := map[string][]int{}
	for name, b := range scope.values {
		if b.IsOverloaded() {
			continue
		}
		fn, isFunc := schemeType(b.Schemes[0]).(*soltype.FuncType)
		if !isFunc {
			continue
		}
		for i, p := range fn.Params {
			if p.Moved {
				moved[name] = append(moved[name], i)
			}
		}
	}
	return moved
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
  m(&self, a: {x: number}) -> number,
  m(&self, a: {x: number}, b: number) -> number,
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
  constructor(&mut self, a: {x: number}),
  constructor(&mut self, a: {x: number}, b: number),
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
  constructor(&mut self, a: number),
  constructor(&mut self, a: number, b: number),
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

// TestMeetFuncsKeepsMovedMarker asserts that meeting two arrows with equal parameter types
// marks a parameter Moved when only the second arrow's parameter is Moved, and leaves the
// first arrow's parameters unchanged. The returns differ so the two arrows are not equal
// and the meet builds a new arrow.
func TestMeetFuncsKeepsMovedMarker(t *testing.T) {
	param := func(moved bool) *soltype.FuncParam {
		return &soltype.FuncParam{
			Pattern: &soltype.IdentPat{Name: "p"},
			Type:    &soltype.ObjectType{},
			Moved:   moved,
		}
	}
	a := &soltype.FuncType{
		Params: []*soltype.FuncParam{param(false)},
		Ret:    &soltype.PrimType{Prim: soltype.NumPrim},
	}
	b := &soltype.FuncType{
		Params: []*soltype.FuncParam{param(true)},
		Ret:    &soltype.PrimType{Prim: soltype.StrPrim},
	}

	met, ok := (&Context{}).meetFuncs(a, b)
	require.True(t, ok)
	fn, isFunc := met.(*soltype.FuncType)
	require.True(t, isFunc)
	require.True(t, fn.Params[0].Moved)
	require.False(t, a.Params[0].Moved)
}

// TestMovedMarkerDistinguishesArrows asserts that two arrows differing only in a parameter's
// Moved marker compare unequal and sort apart, and that their meet keeps the marker.
func TestMovedMarkerDistinguishesArrows(t *testing.T) {
	arrow := func(moved bool) *soltype.FuncType {
		return &soltype.FuncType{
			Params: []*soltype.FuncParam{{
				Pattern: &soltype.IdentPat{Name: "p"},
				Type:    &soltype.ObjectType{},
				Moved:   moved,
			}},
			Ret: &soltype.UndefinedType{},
		}
	}
	plain, moved := arrow(false), arrow(true)

	require.False(t, equalType(plain, moved))
	require.NotZero(t, compareType(plain, moved))

	met, ok := (&Context{}).meetFusedAtoms(plain, moved)
	require.True(t, ok)
	fn, isFunc := met.(*soltype.FuncType)
	require.True(t, isFunc)
	require.True(t, fn.Params[0].Moved)
}
