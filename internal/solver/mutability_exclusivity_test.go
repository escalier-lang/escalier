package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The tests in this file check, from source, the rule that a value is never reachable
// through a mutable path and an immutable path at the same time. A write through the
// owner while an immutable borrow of it is live, or an immutable borrow taken while a
// mutable borrow is live, breaks it. Binding an owned value into another owned binding
// moves it instead, so several cases assert a use-after-move.

// TestStaticEscapeTransitionFromSource asserts that a write through a borrow parameter
// conflicts with a `'static` copy of it. The store copies the borrow `p` into the module-level `sink`, as D8 decides, and the copy is `'static`. sink
// keeps an immutable path to p for the rest of the program, so the write through p breaks E2.
func TestStaticEscapeTransitionFromSource(t *testing.T) {
	_, _, errs := inferSource(t, `
		var sink = &{x: 0}
		fn cache(p: &mut {x: number}) {
			sink = p
			p.x = 1
		}
	`)
	require.Equal(t,
		[]string{"5:4-5:7: cannot assign to 'p.x' while it is borrowed as immutable"},
		messagesWithSpan(t, errs))
}

// TestGlobalWriteMutTransition covers stores into a module-level binding. A store of an
// owned value moves it into the binding. The check runs within one body, so it does not
// catch a caller that keeps a mutable path to a value stored into an immutable global. The
// dead-source case below shows that gap.
func TestGlobalWriteMutTransition(t *testing.T) {
	// Storing an owned-mutable value into the global `sink` moves it: the store
	// transfers ownership into the permanent storage location, so the later `p.x = 5` is a
	// use-after-move. This is the motivating `leak` example — the move consumes p, so
	// the mutation that would let `sink` observe a later write is rejected.
	t.Run("mut_into_immutable_global_then_mutate_error", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			var sink = {x: 0}
			fn f(mut p: {x: number}) {
				sink = p
				p.x = 5
			}
		`)
		require.Equal(t, []string{
			"5:5-5:6: use of moved value 'p'",
		}, messagesWithSpan(t, errs))
	})

	// When the source is dead within this body, the in-body check reports
	// nothing. This is NOT a soundness guarantee. The store still escapes p to 'static,
	// and the CALLER may keep a live mutable alias to the same value and mutate it after
	// the call, so the immutable `sink` observes a mutation. Catching that needs the call
	// site to enforce the 'static borrow as unique, which is the borrow checker's job
	// (#618, #762), not this pass. The assertion covers current behavior and is expected to
	// gain an error once the caller-side check lands.
	t.Run("dead_in_body_source_no_inbody_conflict", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			var sink = {x: 0}
			fn f(mut p: {x: number}) {
				sink = p
			}
		`)
		require.Empty(t, errs)
	})

	// Storing a FRESH value into a global moves no binding, so nothing is reported.
	t.Run("fresh_value_into_global_ok", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			var sink = {x: 0}
			fn f() {
				sink = {x: 9}
			}
		`)
		require.Empty(t, errs)
	})
}

// TestTransitionReassignNestedRHS asserts that a reassignment whose RHS contains
// statements, such as `b = if cond { … } else { … }`, reports no spurious error. Walking
// that RHS re-enters inferStmt, which overwrites c.fn.currentStmt. The reassignment's
// records use the statement captured before the RHS walk, so they resolve the correct CFG
// StmtRef.
func TestTransitionReassignNestedRHS(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn test(cond: boolean) {
			var b = 0
			b = if cond { 1 } else { 2 }
			b
		}
	`)
	require.Empty(t, errs)
}

// TestTransitionWiringNoSpuriousErrors asserts that the liveness pre-pass and the
// ownership checks run over real bodies without inventing an error. Each case type-checks
// cleanly, so any diagnostic would be a wiring bug. The cases cover declarations that copy
// or join their sources, and parameters of each mutability.
func TestTransitionWiringNoSpuriousErrors(t *testing.T) {
	tests := map[string]string{
		// An immutable owned object moved down a chain of bindings.
		"immutable_chain": `
			fn test() {
				val a = {x: 1}
				val b = a
				val c = b
				c
			}
		`,
		// An immutable parameter bound to a val.
		"single_source_alias": `
			fn test(q: {y: number}) {
				val r = q
				r
			}
		`,
		// An if/else over two parameters, bound to one val.
		"multi_source_alias": `
			fn test(cond: boolean, a: {x: number}, b: {x: number}) {
				val c = if cond { a } else { b }
				c
			}
		`,
		// A field write through a `mut` parameter beside a read of an immutable one.
		"params_seeded": `
			fn test(mut p: {x: number}, q: {y: number}) {
				p.x = 5
				q.y
			}
		`,
	}
	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, src)
			require.Empty(t, errs)
		})
	}
}

// TestTransitionWiringReportsMoveError is the error counterpart to
// TestTransitionWiringNoSpuriousErrors. It proves the wired body walk reports a real
// use-after-move from source, not just that it stays silent on benign bodies.
//
// p is an owned-mutable parameter. Binding it into the owned `val q: {x} = p` moves
// p and consumes it, so the later `p.x = 5` is a use-after-move.
func TestTransitionWiringReportsMoveError(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn test(mut p: {x: number}) {
			val q: {x: number} = p
			p.x = 5
			q.x
		}
	`)
	require.ElementsMatch(t, []string{
		"4:4-4:5: use of moved value 'p'",
	}, messagesWithSpan(t, errs))
}

// TestMutabilityTransitionsFromSource covers the mutability-exclusivity rule from source.
// Each case mints an owned-mutable value with `val mut x: {…} = {…}`, borrows it with
// `&` or `&mut`, and asserts the diagnostics. The cases borrow explicitly, since binding
// an owned value into another owned binding moves it, which the move tests cover. want is
// empty for the safe cases.
func TestMutabilityTransitionsFromSource(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// An error when the owner is written while an immutable borrow of it is live.
		"Rule1_SourceLive_Error": {
			src: `
				fn test() {
					val mut items: {x: number} = {x: 1}
					val snapshot: &{x: number} = &items
					items.x = 2
					snapshot
				}
			`,
			want: []string{
				"5:6-5:13: cannot assign to 'items.x' while it is borrowed as immutable",
			},
		},
		// A `do` or a `try` produces what its branches produce, so each branch that yields
		// items has to write the borrow.
		"Rule1_SourceLiveThroughDo_Error": {
			src: `
				fn test() {
					val mut items: {x: number} = {x: 1}
					val snapshot: &{x: number} = do { items }
					items.x = 2
					snapshot
				}
			`,
			want: []string{
				"4:40-4:45: this value is borrowed by its destination, so write the borrow: `&`",
			},
		},
		"Rule1_SourceLiveThroughTry_Error": {
			src: `
				fn test() {
					val mut items: {x: number} = {x: 1}
					val snapshot: &{x: number} = try { items } catch { e => items }
					items.x = 2
					snapshot
				}
			`,
			want: []string{
				"4:41-4:46: this value is borrowed by its destination, so write the borrow: `&`",
				"4:62-4:67: this value is borrowed by its destination, so write the borrow: `&`",
			},
		},
		// Safe when the owner is not written after the borrow.
		"Rule1_SourceDead_OK": {
			src: `
				fn test() {
					val mut items: {x: number} = {x: 1}
					items.x = 2
					val snapshot: &{x: number} = &items
					snapshot
				}
			`,
		},
		// `val z = p` copies p's borrow into z. `sink = z` copies it into
		// module-level state, so the global holds a `'static` immutable path to p. The
		// error is at the write through p made while that path is live.
		"Rule1_TransitiveAliasEscape_Error": {
			src: `
				var sink = &{x: 0}
				fn f(p: &mut {x: number}) {
				  val z = p
				  sink = z
				  p.x = 1
				}
			`,
			want: []string{
				"6:7-6:10: cannot assign to 'p.x' while it is borrowed as immutable",
			},
		},
		// G3 binds a `mut` borrow into a bare annotation by reborrowing it as a local
		// immutable view. `snap` dies within `p`'s region and never escapes, so the
		// lifetime sort accepts it.
		"UnforcedLifetime_LocalReborrow_OK": {
			src: `
				fn f(mut p: {x: number}) {
					val snap: {x: number} = p
				}
			`,
		},
		// Storing the owned `p` into the global `sink` moves it, so binding `p` again
		// into `snap` is a use-after-move. The bind itself takes the immutable→mutable
		// upgrade, since `p` is a uniquely-owned place, so the move is the only error.
		"Rule2_ImmEscape_SourceDead_Error": {
			src: `
				var sink = {x: 0}
				fn f(p: {x: number}) {
				  sink = p
				  val mut snap: {x: number} = p
				  snap.x = 5
				}
			`,
			want: []string{
				"5:35-5:36: use of moved value 'p'",
			},
		},
		// Two mutable borrows of the same value are allowed.
		"Rule3_MultipleMutableAliases_OK": {
			src: `
				fn test() {
					val mut a: {x: number} = {x: 1}
					val b: &mut {x: number} = &mut a
					b.x = 2
					a.x
				}
			`,
		},
		// c copies the borrow b holds, narrowed to `&`, so the write to a.x while c is live
		// conflicts with c.
		"ChainAlias_TargetLive_Error": {
			src: `
				fn test() {
					val mut a: {x: number} = {x: 1}
					val b: &mut {x: number} = &mut a
					val c: &{x: number} = b
					a.x = 2
					c
				}
			`,
			want: []string{
				"6:6-6:9: cannot assign to 'a.x' while it is borrowed as immutable",
			},
		},
		// c's annotation is owned, so by F2 the `if` moves a into c on the branch that yields
		// it. The write to a is a use after that move.
		"Conditional_IfElse_Error": {
			src: `
				fn test(cond: boolean) {
					val mut a: {x: number} = {x: 0}
					val mut b: {x: number} = {x: 1}
					val c: {x: number} = if cond { a } else { b }
					a.x = 5
					c
				}
			`,
			want: []string{"6:6-6:7: use of moved value 'a'"},
		},
		// Safe when the immutable borrow is dead. snapshot is never read, so the write to
		// items does not overlap it.
		"Rule1_TargetDead_OK": {
			src: `
				fn test() {
					val mut items: {x: number} = {x: 1}
					val snapshot: &{x: number} = &items
					items.x = 2
				}
			`,
		},
		// c takes a or b by move. Writing c while the immutable borrow frozen is live is
		// reported once.
		"Conditional_SourceMutInTwoSets_Error": {
			src: `
				fn test(cond: boolean) {
					val mut a: {x: number} = {x: 0}
					val mut b: {x: number} = {x: 1}
					val mut c: {x: number} = if cond { a } else { b }
					val frozen: &{x: number} = &c
					c.x = 3
					frozen
				}
			`,
			want: []string{
				"7:6-7:9: cannot assign to 'c.x' while it is borrowed as immutable",
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

// TestRule2TransitionFromSource covers binding an immutable value into an owned `mut`
// destination while the source stays live afterward. The bind takes the immutable→mutable
// upgrade — config is a uniquely-owned place, so granting mutableConfig the mutable type
// aliases nothing live — and the move consumes config, so the later `config.x` read is a
// use-after-move. Owned-into-owned is a move under the affine rule, so the move is the only
// error. There is no exclusivity error or immutable<:mutable error.
func TestRule2TransitionFromSource(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn test() {
			val config: {x: number} = {x: 1}
			val mut mutableConfig: {x: number} = config
			mutableConfig.x = 5
			config.x
		}
	`)
	require.Equal(t, []string{
		"6:4-6:12: use of moved value 'config'",
	}, messagesWithSpan(t, errs))
}

// TestMutabilityTransitionReassignFromSource covers the `x = e` reassignment branch of
// inferAssign, which TestMutabilityTransitionsFromSource never walks because it binds only
// through declarations. The fresh-literal upgrade mints the owned-mutable source these
// cases reassign from.
func TestMutabilityTransitionReassignFromSource(t *testing.T) {
	t.Run("source_live_error", func(t *testing.T) {
		// Reassigning the owned `items` into the owned `snap` destination moves it: snap drops
		// its old value and takes ownership of items, so the later `items.x = 2` is a
		// use-after-move.
		_, _, errs := inferSource(t, `
			fn f() {
				var snap: {x: number} = {x: 0}
				val mut items: {x: number} = {x: 1}
				snap = items
				items.x = 2
				snap
			}
		`)
		require.Equal(t, []string{
			"6:5-6:10: use of moved value 'items'",
		}, messagesWithSpan(t, errs))
	})

	t.Run("source_dead_ok", func(t *testing.T) {
		// items is mutated before the reassignment and never again, so it is dead after
		// snap = items and nothing is reported.
		_, _, errs := inferSource(t, `
			fn f() {
				var snap: {x: number} = {x: 0}
				val mut items: {x: number} = {x: 1}
				items.x = 2
				snap = items
				snap
			}
		`)
		require.Empty(t, messagesWithSpan(t, errs))
	})
}

// TestBorrowPhaseExclusion covers the borrow-phase rule. A mutable owned value sits in
// one of two phases that never overlap. The immutable phase holds any number of `&`
// borrows. The mutable phase holds any number of `&mut` borrows. An immutable borrow
// taken while a mutable borrow of the same value is live crosses the phases and is
// rejected, while several mutable borrows of one value coexist.
func TestBorrowPhaseExclusion(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// An immutable borrow of a mutable owned value is legal on its own. The borrow
		// puts the value in the immutable phase for its lifetime, and no mutable access
		// overlaps it here, so there is no phase conflict.
		"ImmutableBorrowOfMutable_OK": {
			src: `
				fn test() {
					val mut a: {x: number} = {x: 1}
					val s: &{x: number} = &a
					s
				}
			`,
		},
		// An immutable borrow `s` taken while the mutable borrow `m` is still live mixes
		// the immutable and mutable phases, so it is rejected. `m` is the live mutable
		// access the message names.
		"ImmutableBorrowWhileMutableLive_Error": {
			src: `
				fn test() {
					val mut a: {x: number} = {x: 1}
					val m: &mut {x: number} = &mut a
					val s: &{x: number} = &a
					m.x = 2
					s
				}
			`,
			want: []string{
				"5:28-5:30: cannot borrow 'a' as immutable while it is borrowed as mutable",
			},
		},
		// Two mutable borrows of one value stay within the mutable phase, so both are
		// legal. Escalier is single-threaded, so simultaneous mutable borrows race
		// nothing. The phase rule forbids only mixing the two kinds.
		"MultipleMutableBorrows_OK": {
			src: `
				fn test() {
					val mut a: {x: number} = {x: 1}
					val b: &mut {x: number} = &mut a
					val c: &mut {x: number} = &mut a
					b.x = 2
					c.x = 3
					a.x
				}
			`,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

// TestPhaseConflictUnderConditionalMove covers a borrow and a write of a value that is
// moved on some or all paths reaching them. Each use of the moved value is reported as a
// use-after-move, and the write is not reported a second time as a conflict with the
// borrow.
func TestPhaseConflictUnderConditionalMove(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// `a` is moved on the `if` arm and untouched on the `else` arm, so it is moved on
		// some but not all paths reaching the `val snapshot` borrow. Each later use of `a`,
		// the borrow and the write, reads as a conditional use-after-move. The write also
		// conflicts with `snapshot` on the paths that did not move `a`, but the use-after-move
		// already blames that write, so it is reported once.
		"SomePathsMove_WriteReportedOnce": {
			src: `
				fn sink(mut p: {x: number}) {}
				fn test(cond: boolean) {
					val mut a: {x: number} = {x: 1}
					if cond {
						sink(a)
					} else {
						a.x = 1
					}
					val snapshot: &{x: number} = &a
					a.x = 2
					snapshot
				}
			`,
			want: []string{
				"10:36-10:37: use of moved value 'a'",
				"11:6-11:7: use of moved value 'a'",
			},
		},
		// `a` is moved unconditionally before the borrow, so it is moved on every path
		// reaching it. The later uses of `a` are reported as use-after-move, and nothing
		// else is reported alongside them.
		"AllPathsMove_ConflictSubsumed": {
			src: `
				fn sink(mut p: {x: number}) {}
				fn test() {
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
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}
