package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// F1 — the `for (x in xs)` / `for await (x in xs)` iteration protocol. The loop
// variable binds at the iterable's element type. M5 resolves that element type
// structurally over the types the solver can represent — a tuple, the stand-in
// for an array, and a union of tuples — since Array<T> and the `[Symbol.iterator]`
// protocol land in M7. The element type is surfaced through a function's return
// type: a `return x` inside the loop makes the loop variable's type the function's
// return type, so `values["f"]` renders it.

func TestInferForInElementType(t *testing.T) {
	tests := map[string]struct {
		src  string
		want map[string]string
	}{
		// A tuple yields the union of its element types, so iterating `[1, 2, 3]` binds
		// the loop variable at `1 | 2 | 3`.
		"TupleLiteralElementUnion": {
			src: `
				fn f() {
					for x in [1, 2, 3] {
						return x
					}
				}
			`,
			want: map[string]string{"f": "fn () -> 1 | 2 | 3"},
		},
		// A single-element tuple binds the loop variable at that element's type — the
		// milestone's `for (x in numbers)` where `numbers: Array<number>` binds
		// `x: number`, expressed with the tuple that stands in for the array.
		"SingleElementTupleBindsElement": {
			src: `
				fn f(xs: [number]) {
					for x in xs {
						return x
					}
				}
			`,
			want: map[string]string{"f": "fn (xs: [number]) -> number"},
		},
		// A heterogeneous tuple yields the union of its element types.
		"HeterogeneousTupleElementUnion": {
			src: `
				fn f(xs: [number, string]) {
					for x in xs {
						return x
					}
				}
			`,
			want: map[string]string{"f": "fn (xs: [number, string]) -> number | string"},
		},
		// An inexact tuple `[number, ...]` has an open tail of unknown extra elements, so
		// its element union stays inexact — `number | ...`, not a bare `number` — since
		// the loop variable may also be some unlisted tail element.
		"InexactTupleElementUnionInexact": {
			src: `
				fn f(xs: [number, ...]) {
					for x in xs {
						return x
					}
				}
			`,
			want: map[string]string{"f": "fn (xs: [number, ...]) -> number | ..."},
		},
		// The empty tuple has no elements, so nothing can be bound to the loop variable
		// and the body is statically unreachable. The `return x` never runs, so the
		// function falls through to void — not `never`, which would unsoundly claim the
		// function never returns.
		"EmptyTupleBodyUnreachable": {
			src: `
				fn f(xs: []) {
					for x in xs {
						return x
					}
				}
			`,
			want: map[string]string{"f": "fn (xs: []) -> void"},
		},
		// A union of tuples yields the union of the branches' element types, since a
		// union is iterable when every branch is.
		"UnionOfTuplesElementUnion": {
			src: `
				fn f(xs: [number] | [string]) {
					for x in xs {
						return x
					}
				}
			`,
			want: map[string]string{"f": "fn (xs: [number] | [string]) -> number | string"},
		},
		// A borrowed iterable is peeled first, so `for x in &xs` binds the same element
		// type as `for x in xs`.
		"BorrowedIterablePeeled": {
			src: `
				fn f(xs: [number]) {
					for x in &xs {
						return x
					}
				}
			`,
			want: map[string]string{"f": "fn (xs: [number]) -> number"},
		},
		// Nested loops each resolve their own iterable; the inner loop variable is the
		// one returned here.
		"NestedLoops": {
			src: `
				fn f(xs: [number], ys: [string]) {
					for x in xs {
						for y in ys {
							return y
						}
					}
				}
			`,
			want: map[string]string{"f": "fn (xs: [number], ys: [string]) -> string"},
		},
		// A `for mut x` loop variable binds cleanly — bindPattern reads the `mut` marker
		// off the pattern against the owned tuple-element scrutinee.
		"ForMutLoopVariable": {
			src: `
				fn f(xs: [number]) {
					for mut x in xs {
					}
				}
			`,
			want: map[string]string{"f": "fn (xs: [number]) -> void"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Empty(t, errs)
			require.Equal(t, tc.want, values)
		})
	}
}

// TestInferForInErrors covers the loop forms F1 rejects: a non-iterable operand, a
// `for await` outside an async function, and a `for await` over a synchronously
// iterable operand.
func TestInferForInErrors(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// A number is not iterable, so `for (x in 5)` is rejected. The operand renders
		// as its literal type in the message.
		"NumberNotIterable": {
			src: `
				fn f() {
					for x in 5 {
					}
				}
			`,
			want: []string{`3:15-3:16: 5 is not iterable`},
		},
		// A plain object is not iterable — the M7 `[Symbol.iterator]` protocol is what a
		// class instance would satisfy, and it is out of scope until then.
		"ObjectNotIterable": {
			src: `
				fn f(o: {x: number}) {
					for x in o {
					}
				}
			`,
			want: []string{`3:15-3:16: object is not iterable`},
		},
		// `for await` is a walk rejection outside an async function, symmetric to
		// awaiting outside async. Only the walk error is reported, not a second protocol
		// failure.
		"ForAwaitOutsideAsync": {
			src: `
				fn f(xs: [number]) {
					for await x in xs {
					}
				}
			`,
			want: []string{`3:6-4:7: for await can only be used inside an async function`},
		},
		// Inside an async function, `for await` over a synchronously iterable tuple is
		// rejected by the type rule: a tuple is a sync iterable, not an async iterable.
		"ForAwaitOverSyncIterable": {
			src: `
				async fn f(xs: [number]) {
					for await x in xs {
					}
				}
			`,
			want: []string{`3:21-3:23: tuple is not an async iterable`},
		},
		// An iterable that itself failed to infer is the ErrorType recovery
		// placeholder, which absorbs rather than cascading a second "not iterable"
		// diagnostic on top of the underlying error.
		"BrokenIterableNoCascade": {
			src: `
				fn f() {
					for x in nope {
					}
				}
			`,
			want: []string{`3:15-3:19: Unknown identifier: nope`},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(errs))
		})
	}
}

// TestForInBackEdgeMoves confirms the affine move engine sees the loop back edge: a
// value moved in the loop body is a use-after-move on the next iteration, which the
// engine catches because it replays every use against the branch-merged consumed
// lattice, loop back edges included. A `for` loop is the first inferable source that
// gives the CFG a back edge, so this is where AnalyzeMoves first meets one from real
// code.
func TestForInBackEdgeMoves(t *testing.T) {
	// `val q = p` moves p out on the first iteration; the back edge re-enters the body
	// with p already moved, so the same statement on the next iteration reads a moved
	// value.
	src := `
		fn f(xs: [number]) {
			val mut p = {x: 0}
			for x in xs {
				val q = p
			}
		}
	`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{`5:13-5:14: use of moved value 'p'`}, messagesWithSpan(errs))
}

// TestForInBorrowAvoidsMove pairs each move that a loop makes into a use-after-move
// with its `&`-borrow counterpart, which reads the source without consuming it and
// so type-checks cleanly. Borrowing is how a loop reads a value it must keep usable
// across iterations or after the loop.
func TestForInBorrowAvoidsMove(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// Moving p into q inside the body consumes p; the back edge re-enters the body
		// with p already moved, so the next iteration's `val q = p` reads a moved value.
		"MoveReusedAcrossBackEdge": {
			src: `
				fn f(xs: [number]) {
					val mut p = {x: 0}
					for x in xs {
						val q = p
					}
				}
			`,
			want: []string{`5:15-5:16: use of moved value 'p'`},
		},
		// Borrowing p leaves it live, so re-entering the body across the back edge finds
		// p usable — no use-after-move.
		"BorrowReusedAcrossBackEdge": {
			src: `
				fn f(xs: [number]) {
					val mut p = {x: 0}
					for x in xs {
						val q = &p
					}
				}
			`,
			want: nil,
		},
		// Moving p inside the loop leaves it moved after the loop, so reading `p.x` once
		// the loop exits is a use-after-move — and the next-iteration re-read is one too.
		"MoveThenUsedAfterLoop": {
			src: `
				fn f(xs: [number]) {
					val mut p = {x: 0}
					for x in xs {
						val q = p
					}
					p.x
				}
			`,
			want: []string{
				`5:15-5:16: use of moved value 'p'`,
				`7:6-7:9: use of moved value 'p'`,
			},
		},
		// Borrowing p inside the loop keeps it owned, so reading `p.x` after the loop is
		// valid.
		"BorrowThenUsedAfterLoop": {
			src: `
				fn f(xs: [number]) {
					val mut p = {x: 0}
					for x in xs {
						val q = &p
					}
					p.x
				}
			`,
			want: nil,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(errs))
		})
	}
}

// TestForInBackEdgeBorrows confirms the flow-sensitive borrow-edge dataflow
// (analyzeBorrows) joins across the loop back edge and clears referents correctly.
// These are the loop analogues of the branch-merge borrow tests, exercising the
// back-edge join, the whole-binding kill, and the field-store subtree kill.
func TestForInBackEdgeBorrows(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// A borrow repointed inside the loop reaches the loop merge alongside the
		// pre-loop referent: with zero iterations a still borrows c, and after the body a
		// borrows d, so the union at the exit escapes both locals.
		"ReassignInLoopUnionsAtMerge": {
			src: `
				fn f(xs: [number]) {
					val mut c = {value: 1}
					val mut d = {value: 0}
					var a = &mut c
					for x in xs {
						a = &mut d
					}
					return a
				}
			`,
			want: []string{
				`9:13-9:14: borrowed value 'c' does not live long enough to escape the function`,
				`9:13-9:14: borrowed value 'd' does not live long enough to escape the function`,
			},
			types: map[string]string{"f": "fn (xs: [number]) -> &mut {value: number}"},
		},
		// A whole-binding reassignment after the loop clears every referent the loop
		// carried into it: `a = &mut d` replaces a's whole edge set, so returning a
		// escapes only d, not the c the loop body kept repointing to. This is
		// clearEagerSubtree's unconditional kill clearing a referent that reaches the
		// reassignment through the back edge.
		"PostLoopReassignClearsLoopEdges": {
			src: `
				fn f(xs: [number]) {
					val mut c = {value: 1}
					val mut d = {value: 0}
					var a = &mut c
					for x in xs {
						a = &mut c
					}
					a = &mut d
					return a
				}
			`,
			want:  []string{`10:13-10:14: borrowed value 'd' does not live long enough to escape the function`},
			types: map[string]string{"f": "fn (xs: [number]) -> &mut {value: number}"},
		},
		// A field store inside the loop repoints only the stored field's subtree, so
		// returning the carrier component-moves the stored local and re-anchors it in the
		// returned tree rather than escaping. The prune-gated subtree kill holds across
		// the back edge: it does not over-report a false escape.
		"FieldStoreInLoopComoves": {
			src: `
				fn f(xs: [number]) {
					val mut c = {x: 1}
					val mut d = {x: 0}
					val mut b = {peer: &mut c}
					for x in xs {
						b.peer = &mut d
					}
					return b
				}
			`,
			want:  nil,
			types: map[string]string{"f": "fn (xs: [number]) -> mut {peer: {x: number}}"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(errs))
			require.Equal(t, tc.types, values)
		})
	}
}
