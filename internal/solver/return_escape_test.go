package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/stretchr/testify/require"
)

// TestReturnEscape covers the return-escape rule: a value flowing out of the frame
// may not borrow a function-local, since the local dies when the frame returns. A borrow
// of a parameter is exempt, because its lifetime is supplied by the caller and already
// outlives the return. Each case also pins the function's inferred type, since the escape
// is reported alongside ordinary inference rather than replacing it.
//
// Every case here is a bare borrow flowing out: the outgoing value IS a borrow, so it has
// no owned graph to re-anchor and stays an escape. An owned value carrying a self-contained
// graph of borrowed locals is a connected-component move instead, covered by
// TestConnectedComponentMove.
func TestReturnEscape(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// Returning the borrow itself, `return &mut b`, escapes the local b.
		"ReturnDirectBorrowOfLocal": {
			src: `
				fn build() {
					val mut b = {value: 2}
					return &mut b
				}
			`,
			want:  []string{"4:13-4:19: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"build": "fn () -> &mut {value: number}"},
		},
		// Returning the borrow field itself escapes the local it holds: `a.peer` is the
		// `&mut b` borrow, so returning it leaves b dangling. The field-granular edge at
		// [peer] is followed for the field return.
		"ReturnBorrowFieldEscapes": {
			src: `
				fn build() -> &mut {value: number} {
					val mut b = {value: 2}
					val a = {peer: &mut b, data: {value: 7}}
					return a.peer
				}
			`,
			want:  []string{"5:13-5:19: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"build": "fn () -> &mut {value: number}"},
		},
		// Reading a field through a whole-binding borrow escapes the borrowed local: `a`
		// borrows all of b, so `a.peer` projects into b and returning it leaves b dangling.
		// The edge a → b sits at path [], above the read path [peer], so the field return
		// still follows it.
		"ReturnFieldThroughWholeBorrowEscapes": {
			src: `
				fn build() -> &mut {value: number} {
					val mut b = {peer: {value: 0}}
					val a = &mut b
					return a.peer
				}
			`,
			want:  []string{"5:13-5:19: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"build": "fn () -> &mut {value: number}"},
		},
		// A borrow of a local written inside a control-flow carrier escapes the same as one
		// written directly: the scan descends the if/else branches to find the `&mut b`.
		"ReturnBorrowInIfBranchEscapes": {
			src: `
				fn build() -> &mut {value: number} {
					val mut b = {value: 0}
					return if true { &mut b } else { &mut b }
				}
			`,
			want:  []string{"4:12-4:47: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"build": "fn () -> &mut {value: number}"},
		},
		// Returning a disjoint owned field of a binding that also has a borrow field does
		// not escape: `a.data` carries none of a's borrow of b. The field return follows
		// only the edges at [data], finds none, and reports no spurious escape.
		"ReturnDisjointFieldOk": {
			src: `
				fn build() -> {value: number} {
					val mut b = {value: 2}
					val a = {peer: &mut b, data: {value: 7}}
					return a.data
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> {value: number}"},
		},
		// Returning a parameter borrow is sound: the borrow carries the caller's lifetime,
		// which outlives the call.
		"ReturnParamBorrowOk": {
			src: `
				fn pass(p: &mut {x: number}) {
					return p
				}
			`,
			want:  nil,
			types: map[string]string{"pass": "fn <'a>(p: &'a mut {x: number}) -> &'a mut {x: number}"},
		},
		// Borrowing a parameter and returning the borrow is sound for the same reason.
		"ReturnBorrowOfParamOk": {
			src: `
				fn pass(p: mut {x: number}) {
					return &mut p
				}
			`,
			want:  nil,
			types: map[string]string{"pass": "fn (p: mut {x: number}) -> &mut {x: number}"},
		},
		// A local that only borrows a parameter is returnable: the edge to the parameter
		// is never recorded, so returning the local raises no escape.
		"LocalBorrowsParamThenReturnOk": {
			src: `
				fn pass(p: &mut {x: number}) {
					val a = {peer: p}
					return a
				}
			`,
			want:  nil,
			types: map[string]string{"pass": "fn <'a>(p: &'a mut {x: number}) -> {peer: &'a mut {x: number}}"},
		},
		// A borrow projected into a destructuring leaf escapes: `val {peer} = {peer: &mut
		// b}` binds peer to the borrow of the local b, so returning peer escapes b. The
		// pattern leaf is matched to its initializer property and its edge recorded.
		"ReturnDestructuredBorrowLeafEscapes": {
			src: `
				fn f() -> &mut {value: number} {
					val mut b = {value: 0}
					val {peer} = {peer: &mut b}
					return peer
				}
			`,
			want:  []string{"5:13-5:17: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"f": "fn () -> &mut {value: number}"},
		},
		// Destructuring from a place carries the place's field edges into the leaf: `val
		// {peer} = a` binds peer to a.peer, so peer inherits a's borrow of b at [peer] and
		// returning peer escapes b.
		"ReturnDestructuredFromPlaceEscapes": {
			src: `
				fn build() -> &mut {value: number} {
					val mut b = {value: 0}
					val a = {peer: &mut b}
					val {peer} = a
					return peer
				}
			`,
			want:  []string{"6:13-6:17: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"build": "fn () -> &mut {value: number}"},
		},
		// A shorthand destructuring default escapes: when `obj` may lack peer, `val {peer =
		// &mut b} = obj` binds peer to the default `&mut b` on the absent-property path, so
		// returning peer escapes the local b.
		"ReturnShorthandDefaultEscapes": {
			src: `
				fn f(obj: {peer?: &mut {value: number}}) -> &mut {value: number} {
					val mut b = {value: 0}
					val {peer = &mut b} = obj
					return peer
				}
			`,
			want:  []string{"5:13-5:17: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"f": "fn <'a>(obj: {peer?: &'a mut {value: number}}) -> &'a mut {value: number}"},
		},
		// A borrow introduced by reassigning a `var` escapes: `a = &mut b` records the edge
		// a → b, so returning a escapes the local b. The edge graph accumulates, so the
		// reassignment adds the edge to a's earlier ones.
		// PR 16: flow-sensitive set-and-clear replaces the accumulate-only edge, so the
		// reassignment clears a's prior edges and sets a → b. The escape result is unchanged
		// here, since a's prior referent was the parameter seed, which records no edge; the
		// comment's "accumulates" narrative is what updates.
		"ReturnVarReassignedToBorrowEscapes": {
			src: `
				fn f(seed: &mut {value: number}) {
					var a = seed
					val mut b = {value: 0}
					a = &mut b
					return a
				}
			`,
			want:  []string{"6:13-6:14: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"f": "fn <'a>(seed: &'a mut {value: number}) -> &'a mut {value: number}"},
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

// TestEscapeAtStoreAndArgSites covers the other two flow-out sites: a field store
// into a parameter, where the value flows into the caller's object, and a consuming
// argument, where it flows into the callee. A borrow of a local that flows out either
// way escapes, while a parameter borrow and a plain owned value do not. Each case also
// pins the inferred type of every function it declares.
func TestEscapeAtStoreAndArgSites(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// Storing a borrow of a local into a parameter's field escapes: the parameter's
		// object outlives the frame, so the stored local would dangle in the caller.
		"StoreLocalBorrowIntoParamField": {
			src: `
				fn f(p: mut {peer: &mut {value: number}}) {
					val mut b = {value: 0}
					p.peer = &mut b
				}
			`,
			want:  []string{"4:15-4:21: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"f": "fn (p: mut {peer: &mut {value: number}}) -> void"},
		},
		// Storing a parameter borrow into a parameter's field is sound: the stored borrow
		// carries the caller's lifetime, which outlives the frame.
		"StoreParamBorrowIntoParamFieldOk": {
			src: `
				fn f(p: mut {peer: &mut {value: number}}, q: &mut {value: number}) {
					p.peer = q
				}
			`,
			want:  nil,
			types: map[string]string{"f": "fn (p: mut {peer: &mut {value: number}}, q: &mut {value: number}) -> void"},
		},
		// Storing an owned carrier that holds a local borrow into a parameter's field is a
		// connected-component move, not an escape: the stored `{peer: &mut b}` owns a
		// self-contained graph whose only borrowed local b is reached just through it, so the
		// store re-anchors the component to the parameter's region and consumes b. No escape
		// fires, and reading b afterward is a use-after-move. This is the owned-carrier twin of
		// StoreLocalBorrowIntoParamField, where the bare borrow `&mut b` had no graph to
		// re-anchor and escaped.
		"StoreCarrierIntoParamFieldMovesComponent": {
			src: `
				fn f(p: mut {node: {peer: &mut {value: number}}}) {
					val mut b = {value: 0}
					p.node = {peer: &mut b}
					val y = b
				}
			`,
			want:  []string{"5:14-5:15: use of moved value 'b'"},
			types: map[string]string{"f": "fn (p: mut {node: {peer: &mut {value: number}}}) -> void"},
		},
		// Auto-borrowing a local into a `&mut` parameter is sound: the parameter borrows
		// for the call rather than consuming, so the local outlives the borrow.
		"BorrowArgToRefParamOk": {
			src: `
				fn read(x: &mut {value: number}) {}
				fn f() {
					val mut b = {value: 0}
					read(&mut b)
				}
			`,
			want: nil,
			types: map[string]string{
				"read": "fn (x: &mut {value: number}) -> void",
				"f":    "fn () -> void",
			},
		},
		// A consuming argument that is a plain owned value carries no borrow, so it moves
		// into the callee with no escape.
		"ConsumingArgOwnedValueOk": {
			src: `
				fn store(x: {value: number}) {}
				fn f() {
					val a = {value: 0}
					store(a)
				}
			`,
			want: nil,
			types: map[string]string{
				"store": "fn (x: {value: number}) -> void",
				"f":     "fn () -> void",
			},
		},
		// A borrow passed to an inner call is consumed by that call, not carried out by
		// the owned value the call yields. Wrapping the call in a consuming call does not
		// escape the local, so the scan must stop at the inner call boundary.
		"BorrowConsumedByInnerCallDoesNotEscape": {
			src: `
				fn read(x: &mut {value: number}) -> {value: number} {
					return {value: 1}
				}
				fn store(y: {value: number}) {}
				fn f() {
					val mut b = {value: 0}
					store(read(&mut b))
				}
			`,
			want: nil,
			types: map[string]string{
				"read":  "fn (x: &mut {value: number}) -> {value: number}",
				"store": "fn (y: {value: number}) -> void",
				"f":     "fn () -> void",
			},
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

// TestConnectedComponentMove covers the connected-component move: an owned value carrying a
// self-contained graph of borrowed locals flows out of the frame as a unit. The borrowed
// locals are reachable only through the graph, so the move re-anchors them to the
// destination region and consumes every binding in the component, rather than reporting an
// escape. When a node is also reachable from a live binding outside the component, the move
// does not apply and the ordinary escape stands.
//
// Cases tagged `PR 16:` change when the flow-sensitivity work lands. Two behaviours move
// them. Return borrow-stripping rewrites a returned `&`/`&mut` of a function-local reached
// exactly once into an owned field, so a tree-shaped component move returns an owned type
// while a diamond or cyclic graph keeps its borrows. The borrow-field owned-mutable upgrade
// makes `&mut` graph carriers constructible, flipping the all-`&mut` diamond from a
// construction error to a component move. See PR 16 in
// planning/affine_semantics/implementation_plan.md.
func TestConnectedComponentMove(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// The canonical case: the owned binding a holds `&mut b`, and nothing outside the
		// {a, b} component references either node, so returning a moves the whole component
		// out. No escape, and a and b are both consumed.
		// PR 16: b is reached once, so borrow-stripping rewrites the return to the owned
		// `{peer: {value: number}}`.
		"ReturnSelfContainedComponent": {
			src: `
				fn build() {
					val mut b = {value: 2}
					val a = {peer: &mut b}
					return a
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> {peer: &mut {value: number}}"},
		},
		// A component with two borrowed locals moves as a unit just the same: both b and c
		// are reachable only through a, so returning a co-moves all three.
		// PR 16: b and c are each reached once, so borrow-stripping rewrites the return to the
		// owned `{p: {x: number}, q: {x: number}}`.
		"ReturnComponentTwoLocals": {
			src: `
				fn build() {
					val mut b = {x: 0}
					val mut c = {x: 1}
					val a = {p: &mut b, q: &mut c}
					return a
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> {p: &mut {x: number}, q: &mut {x: number}}"},
		},
		// The owned carrier may be a fresh literal with no intervening binding: the returned
		// object owns the borrow of b, and b is reachable only through it.
		// PR 16: b is reached once, so borrow-stripping rewrites the return to the owned
		// `{peer: {value: number}}`.
		"ReturnInlineLiteralComponent": {
			src: `
				fn build() {
					val mut b = {value: 2}
					return {peer: &mut b}
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> {peer: &mut {value: number}}"},
		},
		// A whole-binding move carries the graph forward: `val a2 = a` moves a, borrow and
		// all, into a2. The dead a is not a live external reference to b, so returning a2
		// still moves the {a2, b} component out.
		// PR 16: b is reached once, so borrow-stripping rewrites the return to the owned
		// `{peer: {value: number}}`.
		"ReturnMovedCarrierComponent": {
			src: `
				fn build() {
					val mut b = {value: 2}
					val a = {peer: &mut b}
					val a2 = a
					return a2
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> {peer: &mut {value: number}}"},
		},
		// An acyclic shared graph moves out the same way: a holds `&b`, and b is reachable
		// only through a.
		// PR 16: b is reached once, so borrow-stripping rewrites the return to the owned
		// `{peer: {value: number}}` — stripping covers shared `&` borrows as well as `&mut`.
		"ReturnSharedComponent": {
			src: `
				fn build() {
					val mut b = {value: 2}
					val a = {peer: &b}
					return a
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> {peer: &{value: number}}"},
		},
		// A consuming argument moves the component into the callee, which now owns the graph.
		"ConsumingArgComponentMove": {
			src: `
				fn store(x: {peer: &mut {value: number}}) {}
				fn f() {
					val mut b = {value: 0}
					val a = {peer: &mut b}
					store(a)
				}
			`,
			want: nil,
			types: map[string]string{
				"store": "fn (x: {peer: &mut {value: number}}) -> void",
				"f":     "fn () -> void",
			},
		},
		// The component move consumes the borrowed local, not just the carrier: after the
		// graph moves into store, reading b is a use-after-move even though b was never the
		// argument. The carrier a is consumed by the ordinary argument move.
		"ComponentMoveConsumesBorrowedLocal": {
			src: `
				fn store(x: {peer: &mut {value: number}}) {}
				fn f() {
					val mut b = {value: 0}
					val a = {peer: &mut b}
					store(a)
					val y = b
				}
			`,
			want: []string{"7:14-7:15: use of moved value 'b'"},
			types: map[string]string{
				"store": "fn (x: {peer: &mut {value: number}}) -> void",
				"f":     "fn () -> void",
			},
		},
		// An escape through a consuming call inside a return is a component move at the
		// argument: the literal owning `&mut b` moves into id, b reachable only through it.
		// No escape is reported at either the argument or the enclosing return.
		// PR 16: f's returned value borrows b once, so borrow-stripping rewrites f's return to
		// the owned `{peer: {value: number}}`; id keeps its borrow-typed parameter and result.
		"ComponentMoveThroughConsumingCall": {
			src: `
				fn id(y: {peer: &mut {value: number}}) {
					return y
				}
				fn f() {
					val mut b = {value: 0}
					return id({peer: &mut b})
				}
			`,
			want: nil,
			types: map[string]string{
				"id": "fn <'a>(y: {peer: &'a mut {value: number}}) -> {peer: &'a mut {value: number}}",
				"f":  "fn () -> {peer: &mut {value: number}}",
			},
		},
		// A transitive chain moves as a unit: a borrows b, b borrows c, c borrows d, so the
		// component reachable from a is {a, b, c, d}. Returning a co-moves all four. The
		// chain uses shared borrows so the carriers nest without a mutable-view conflict.
		// PR 16: every node is reached once, so borrow-stripping rewrites the return to the
		// owned `{peer: {peer: {peer: {value: 4}}}}`.
		"ReturnTransitiveChain": {
			src: `
				fn build() {
					val d = {value: 4}
					val c = {peer: &d}
					val b = {peer: &c}
					val a = {peer: &b}
					return a
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> {peer: &{peer: &{peer: &{value: 4}}}}"},
		},
		// A diamond shares a node: a borrows b and c, and both b and c borrow d, so d is
		// reachable through two paths. The component is still {a, b, c, d} and moves out as a
		// unit; reaching d twice is collapsed by the reachability walk's seen set.
		// PR 16: d is reached through two paths, so borrow-stripping KEEPS the borrows — an
		// owned tree cannot express the shared node — and this return type is unchanged. This
		// is the multiplicity case that bounds stripping.
		"ReturnDiamondSharedNode": {
			src: `
				fn build() {
					val d = {x: 0}
					val b = {peer: &d}
					val c = {peer: &d}
					val a = {l: &b, r: &c}
					return a
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> {l: &{peer: &{x: 0}}, r: &{peer: &{x: 0}}}"},
		},
		// The mutable analog of the shared diamond does not form. Borrowing `&mut b` and
		// `&mut c` where b and c are owned objects already holding a `&mut` borrow is rejected,
		// since a `&mut` of a borrow-carrying local is not yet supported, so the carrier never
		// builds. The aliasing question the shared diamond raises — d reached through two
		// mutable paths — is therefore never reached here; the two-mutable-alias rejection is
		// pinned separately by MutableAliasRejectsMove.
		// PR 16: the borrow-field owned-mutable upgrade makes the `&mut` carriers
		// constructible, so this flips from the two `cannot constrain` errors to a
		// connected-component move with `want: nil`. d is reached through two paths, so
		// borrow-stripping keeps the borrows and the moved type stays
		// `{l: &mut {peer: &mut {x: number}}, r: &mut {peer: &mut {x: number}}}`. Rename to
		// MutableDiamondMovesAsUnit when it lands.
		"MutableDiamondRejected": {
			src: `
				fn build() {
					val mut d = {x: 0}
					val mut b = {peer: &mut d}
					val mut c = {peer: &mut d}
					val a = {l: &mut b, r: &mut c}
					return a
				}
			`,
			want: []string{
				"6:18-6:24: cannot constrain immutable object <: mutable object",
				"6:29-6:35: cannot constrain immutable object <: mutable object",
			},
			types: map[string]string{"build": "fn () -> {l: &mut {peer: &mut {x: number}}, r: &mut {peer: &mut {x: number}}}"},
		},
		// A node mutably aliased outside the moved component blocks the move: b and c each hold
		// `&mut d`, so returning b cannot move d out while c is a live second mutable path to
		// it. This is the same external-reference rejection as the shared case, and it is what
		// keeps a mutable same-graph alias from being co-moved out from under a live writer.
		"MutableAliasRejectsMove": {
			src: `
				fn build() {
					val mut d = {x: 0}
					val b = {peer: &mut d}
					val c = {peer: &mut d}
					return b
				}
			`,
			want:  []string{"6:13-6:14: borrowed value 'd' does not live long enough to escape the function"},
			types: map[string]string{"build": "fn () -> {peer: &mut {x: number}}"},
		},
		// Two mutable aliases of one node are fine when both live inside the moved component.
		// b and c each hold `&mut d`, and a holds shared borrows of both, so the component
		// reachable from a is {a, b, c, d} and both `&mut d` paths are internal to it. The
		// model permits several `&mut` borrows live at once, so this is a legal value, and
		// returning a re-anchors the whole graph — both internal mutable paths included — with
		// no external observer. This is the mirror of MutableAliasRejectsMove: there the second
		// `&mut d` was left outside the component and rejected; here a owns both, so it is not.
		// The outer borrows are shared, so the carrier sidesteps the owned-mutable upgrade that
		// blocks the all-`&mut` MutableDiamondRejected.
		// PR 16: two changes meet here but cancel. The borrow-field upgrade no longer blocks
		// the all-`&mut` MutableDiamondRejected, so that cross-reference goes stale. And d is
		// reached through two paths, so borrow-stripping keeps the borrows — this return type
		// is unchanged.
		"InternalMutableAliasMovesAsUnit": {
			src: `
				fn build() {
					val mut d = {x: 0}
					val mut b = {peer: &mut d}
					val mut c = {peer: &mut d}
					val a = {l: &b, r: &c}
					return a
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> {l: &{peer: &mut {x: number}}, r: &{peer: &mut {x: number}}}"},
		},
		// The co-move consumes the shared node exactly once despite the two internal mutable
		// paths to it: storing the graph into a callee consumes d, so reading it afterward is a
		// single use-after-move, with no spurious double-move from reaching d through both b
		// and c.
		"InternalMutableAliasConsumesSharedNode": {
			src: `
				fn store(x: {l: &{peer: &mut {x: number}}, r: &{peer: &mut {x: number}}}) {}
				fn f() {
					val mut d = {x: 0}
					val mut b = {peer: &mut d}
					val mut c = {peer: &mut d}
					val a = {l: &b, r: &c}
					store(a)
					val y = d
				}
			`,
			want: []string{"9:14-9:15: use of moved value 'd'"},
			types: map[string]string{
				"store": "fn (x: {l: &{peer: &mut {x: number}}, r: &{peer: &mut {x: number}}}) -> void",
				"f":     "fn () -> void",
			},
		},
		// A wider component with five borrowed locals moves out as one unit, the same as the
		// two-local case, since every node is reachable only through a.
		// PR 16: every node is reached once, so borrow-stripping rewrites the return to the
		// owned `{b1: {x: number}, …}` with all five fields owned.
		"ReturnLargeStar": {
			src: `
				fn build() {
					val mut b = {x: 1}
					val mut c = {x: 2}
					val mut d = {x: 3}
					val mut e = {x: 4}
					val mut g = {x: 5}
					val a = {b1: &mut b, c1: &mut c, d1: &mut d, e1: &mut e, g1: &mut g}
					return a
				}
			`,
			want: nil,
			types: map[string]string{
				"build": "fn () -> {b1: &mut {x: number}, c1: &mut {x: number}, d1: &mut {x: number}, e1: &mut {x: number}, g1: &mut {x: number}}",
			},
		},
		// The co-move reaches the deepest transitive node: storing the chain a → b → c → d
		// into a callee consumes d, so reading it afterward is a use-after-move even though d
		// is three borrows away from the moved binding.
		"ChainMoveConsumesDeepestNode": {
			src: `
				fn store(x: {peer: &{peer: &{peer: &{value: number}}}}) {}
				fn f() {
					val d = {value: 4}
					val c = {peer: &d}
					val b = {peer: &c}
					val a = {peer: &b}
					store(a)
					val y = d
				}
			`,
			want: []string{"9:14-9:15: use of moved value 'd'"},
			types: map[string]string{
				"store": "fn (x: {peer: &{peer: &{peer: &{value: number}}}}) -> void",
				"f":     "fn () -> void",
			},
		},
		// A node also reachable from a live binding outside the component is not self-
		// contained: keep holds a second borrow of b, so the move does not apply and
		// returning a reports the ordinary escape.
		"RetainedNodeRejectsMove": {
			src: `
				fn build() {
					val mut b = {value: 2}
					val a = {peer: &b}
					val keep = &b
					return a
				}
			`,
			want:  []string{"6:13-6:14: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"build": "fn () -> {peer: &{value: number}}"},
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

// TestComponentEscapeCyclicGraph drives the component reachability walk over a cyclic
// borrow-edge graph. A genuine cycle — a borrows b and b borrows a — is not expressible in
// source today, since it needs a recursive type alias (M7) for the mutually-referential
// carriers, and the `.push` form the requirements' cyclic `build()` uses does not record
// borrow edges. So the cycle handling is exercised by building the edge graph directly and
// asserting escapingLocalsOf terminates and returns every node, the root included.
//
// escapingLocalsOf reaching the root back through the cycle is the case resolveComponentEscapes
// guards when it skips the root while consuming co-moved locals, so a cyclic component does
// not double-move its own root.
func TestComponentEscapeCyclicGraph(t *testing.T) {
	edge := func(referent liveness.VarID) fieldBorrow {
		return fieldBorrow{path: nil, referent: referent}
	}
	tests := map[string]struct {
		edges map[liveness.VarID][]fieldBorrow
		root  liveness.VarID
		want  []liveness.VarID
	}{
		// A two-node cycle: a ⇄ b. Reaching b follows b's edge back to a, so the escaping
		// set closes over both nodes and includes the root a.
		"TwoNodeCycle": {
			edges: map[liveness.VarID][]fieldBorrow{
				1: {edge(2)},
				2: {edge(1)},
			},
			root: 1,
			want: []liveness.VarID{1, 2},
		},
		// A three-node cycle: a → b → c → a. Every node is reachable, and the walk's seen set
		// terminates the loop at the third hop rather than recurring forever.
		"ThreeNodeCycle": {
			edges: map[liveness.VarID][]fieldBorrow{
				1: {edge(2)},
				2: {edge(3)},
				3: {edge(1)},
			},
			root: 1,
			want: []liveness.VarID{1, 2, 3},
		},
		// A node with a self-loop plus an onward edge: a → a and a → b. The self-edge is
		// followed once and collapsed by the seen set, and b is still reached.
		"SelfLoopAndOnward": {
			edges: map[liveness.VarID][]fieldBorrow{
				1: {edge(1), edge(2)},
			},
			root: 1,
			want: []liveness.VarID{1, 2},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			c := newChecker()
			c.fn = &funcCtx{
				borrowEdges: tc.edges,
				paramVarIDs: set.NewSet[liveness.VarID](),
			}
			e := &ast.IdentExpr{Name: "root", VarID: int(tc.root)}
			got := c.escapingLocalsOf(e).ToSlice()
			require.ElementsMatch(t, tc.want, got)
		})
	}
}
