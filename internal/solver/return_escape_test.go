package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/stretchr/testify/require"
)

// TestReturnValueBorrows covers what a `return` may carry out of the frame. A borrow of a
// function-local is allowed to leave: the frame is gone afterwards, so the borrow the caller
// receives is the only path left to the value, and the runtime keeps that value alive because
// it is garbage collected. The return is then re-typed to own what it borrowed, since owning
// is what the caller actually holds.
//
// Each case pins the function's inferred type, which is where the re-typing shows. An
// unannotated `return &mut b` over `val mut b` comes out as `mut {value: number}`. A signature
// that annotates its return keeps the annotation, since an owned value satisfies a borrow
// destination.
//
// A borrow of a parameter is a separate exemption and predates this one: its lifetime comes
// from the caller and already outlives the return, so nothing is re-typed.
//
// Re-typing needs the borrow graph reachable from the return to be a tree, meaning no local is
// reached twice. A case that reaches one twice keeps its borrow in the type. TestConnectedComponentMove
// covers the same rule for a return that carries an owned aggregate holding borrows.
func TestReturnValueBorrows(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// The first repro in #1264. The bare borrow `return &mut b` leaves as the only path to
		// b, so the return owns what it borrowed. The owned form is immutable, and the
		// caller's binding decides.
		"ReturnDirectBorrowOfLocal": {
			src: `
				fn build() {
					val mut b = {value: 2}
					return &mut b
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> {value: number}"},
		},
		// A signature may still ask to hand the value out mutably. Every return operand is
		// uniquely owned, so the immutable-to-mutable upgrade grants it.
		"ReturnDirectBorrowUnderAMutAnnotation": {
			src: `
				fn f() -> mut {value: number} {
					val mut q = {value: 1}
					return &mut q
				}
			`,
			want:  nil,
			types: map[string]string{"f": "fn () -> mut {value: number}"},
		},
		// The caller decides mutability, which is what makes the immutable default workable.
		// A plain `val` keeps the returned value frozen; `val mut` thaws it.
		"CallerOptsIntoMutability": {
			src: `
				fn f() {
					val mut q = {value: 1}
					return &mut q
				}
				fn caller() -> undefined {
					val mut r = f()
					r.value = 2
				}
			`,
			want: nil,
			types: map[string]string{
				"f":      "fn () -> {value: number}",
				"caller": "fn () -> undefined",
			},
		},
		// The second repro in #1264. The store puts b at a.peer, and a and b are both dead at
		// the return, so `a.peer` is again the only path out. A field read is not re-typed:
		// the property's type is a variable the evaluator settles after the strip runs, so
		// there is no borrow in hand to rewrite. The move still consumes b.
		"ReturnStoredBorrowThroughACarrierField": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined

				fn build(p: mut {value: number}, q: mut {value: number}) -> &mut {value: number} {
					val mut b = {value: 2}
					val mut a = {peer: &mut p, spare: &mut q}
					store(&mut a, &mut b)
					return a.peer
				}
			`,
			want: nil,
			types: map[string]string{
				"store": "fn <'a>(target: &mut {peer: &'a mut {value: number}, spare: &mut {value: number}}, " +
					"item: &'a mut {value: number}) -> undefined",
				"build": "fn (p: mut {value: number}, q: mut {value: number}) -> &mut {value: number}",
			},
		},
		// Returning the borrow field `a.peer` hands out the only path to b, since a dies with
		// the frame. The field-granular edge at [peer] is what finds b.
		"ReturnBorrowField": {
			src: `
				fn build() -> &mut {value: number} {
					val mut b = {value: 2}
					val a = {peer: &mut b, data: {value: 7}}
					return a.peer
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> &mut {value: number}"},
		},
		// Reading a field through a whole-binding borrow reaches the same place: a borrows all
		// of b, so `a.peer` projects into b. The edge a → b sits at path [], above the read
		// path [peer], so the field return still follows it.
		"ReturnFieldThroughWholeBorrow": {
			src: `
				fn build() -> &mut {value: number} {
					val mut b = {peer: {value: 0}}
					val a = &mut b
					return a.peer
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> &mut {value: number}"},
		},
		// Both branches of the carrier borrow b, so the walk reaches b twice and the graph is
		// not a tree. Only one branch runs, so the escape is still the only path out and the
		// return is accepted; the graph cannot tell the branches apart, so the type keeps its
		// borrow rather than claiming ownership the checker has not proven.
		"ReturnBorrowInIfBranchKeepsItsBorrow": {
			src: `
				fn build() -> &mut {value: number} {
					val mut b = {value: 0}
					return if true { &mut b } else { &mut b }
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> &mut {value: number}"},
		},
		// Returning a disjoint owned field carries no borrow at all: `a.data` follows the
		// edges at [data], finds none, and never reaches the escape decision.
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
		// Returning a parameter borrow is sound for a different reason: the borrow carries the
		// caller's lifetime, which outlives the call. Nothing is re-typed, since the caller
		// still holds its own path to the value.
		"ReturnParamBorrowOk": {
			src: `
				fn pass(p: &mut {x: number}) {
					return p
				}
			`,
			want:  nil,
			types: map[string]string{"pass": "fn <'a>(p: &'a mut {x: number}) -> &'a mut {x: number}"},
		},
		// Borrowing a parameter and returning the borrow keeps the parameter's lifetime too.
		"ReturnBorrowOfParamOk": {
			src: `
				fn pass(p: mut {x: number}) {
					return &mut p
				}
			`,
			want:  nil,
			types: map[string]string{"pass": "fn (p: mut {x: number}) -> &mut {x: number}"},
		},
		// A local that only borrows a parameter records no edge, so the return carries nothing
		// the escape check tracks and the parameter's lifetime reaches the result.
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
		// A borrow projected into a destructuring leaf leaves the same way: `val {peer} =
		// {peer: &mut b}` binds peer to the borrow of b, and peer is the only path out.
		"ReturnDestructuredBorrowLeaf": {
			src: `
				fn f() -> &mut {value: number} {
					val mut b = {value: 0}
					val {peer} = {peer: &mut b}
					return peer
				}
			`,
			want:  nil,
			types: map[string]string{"f": "fn () -> &mut {value: number}"},
		},
		// Destructuring from a place carries the place's field edges into the leaf: `val
		// {peer} = a` binds peer to a.peer, so peer inherits a's borrow of b at [peer].
		"ReturnDestructuredFromPlace": {
			src: `
				fn build() -> &mut {value: number} {
					val mut b = {value: 0}
					val a = {peer: &mut b}
					val {peer} = a
					return peer
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> &mut {value: number}"},
		},
		// A shorthand destructuring default reaches the local on the absent-property path:
		// when `obj` lacks peer, `val {peer = &mut b} = obj` binds peer to `&mut b`.
		"ReturnShorthandDefault": {
			src: `
				fn f(obj: {peer?: &mut {value: number}}) -> &mut {value: number} {
					val mut b = {value: 0}
					val {peer = &mut b} = obj
					return peer
				}
			`,
			want:  nil,
			types: map[string]string{"f": "fn <'a>(obj: {peer?: &'a mut {value: number}}) -> &'a mut {value: number}"},
		},
		// A local this frame also sends out another way is not the return's alone. The store
		// puts a borrow of b in the caller's object, so the caller reaches b through p.node.peer
		// AND through the return: two live mutable paths to one value. The return takes no
		// exemption and reports.
		"ReturnOfALocalAlsoStoredIntoAParam": {
			src: `
				fn f(p: mut {node: {peer: &mut {value: number}}}) {
					val mut b = {value: 0}
					p.node = {peer: &mut b}
					return &mut b
				}
			`,
			want:  []string{"5:13-5:19: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"f": "fn (p: mut {node: {peer: &mut {value: number}}}) -> &mut {value: number}"},
		},
		// A consuming argument leaves the same second path behind, so returning the same local
		// reports for the same reason.
		"ReturnOfALocalAlsoPassedToAConsumingCall": {
			src: `
				declare fn take(x: {peer: &mut {value: number}}) -> undefined
				fn f() {
					val mut b = {value: 0}
					take({peer: &mut b})
					return &mut b
				}
			`,
			want: []string{"6:13-6:19: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{
				"take": "fn (x: {peer: &mut {value: number}}) -> undefined",
				"f":    "fn () -> &mut {value: number}",
			},
		},
		// A store of a DIFFERENT local leaves no path to this one, so the return keeps its
		// exemption. This is the case the previous two must not over-report.
		"ReturnOfALocalWhileAnotherIsStoredOut": {
			src: `
				fn f(p: mut {node: {peer: &mut {value: number}}}) {
					val mut b = {value: 0}
					val mut d = {value: 1}
					p.node = {peer: &mut d}
					return &mut b
				}
			`,
			want:  nil,
			types: map[string]string{"f": "fn (p: mut {node: {peer: &mut {value: number}}}) -> {value: number}"},
		},
		// Two returns of one local on exclusive branches are each the only path, since only one
		// of them runs. Neither is a flow-out the other has to account for.
		"TwoExclusiveReturnsOfOneLocal": {
			src: `
				fn f(cond: boolean) {
					val mut b = {value: 0}
					if cond {
						return &mut b
					}
					return &mut b
				}
			`,
			want:  nil,
			types: map[string]string{"f": "fn (cond: boolean) -> {value: number}"},
		},
		// A function returning a local borrow on one path and a parameter borrow on another
		// keeps both borrowed. A parameter borrow carries no local edge, so it never strips,
		// and owning only the first would union `mut B` with `&'a mut B`, which Escalier
		// rejects for mixing ownership. Holding the rewrite back leaves the function uniform.
		"MixedLocalAndParamReturnsStayBorrowed": {
			src: `
				fn f(p: &mut {value: number}, cond: boolean) {
					val mut b = {value: 0}
					if cond {
						return &mut b
					}
					return p
				}
			`,
			want:  nil,
			types: map[string]string{"f": "fn <'a>(p: &'a mut {value: number}, cond: boolean) -> &'a mut {value: number}"},
		},
		// Stripping reaches through a shared borrow of a carrier that itself holds a `&mut`
		// field, so the returned tree owns c as well as a. The owned form is immutable, like
		// every stripped return: the caller's binding decides mutability.
		"SharedBorrowOfAMutableCarrierOwnsImmutably": {
			src: `
				fn f() {
					val mut c = {value: 1}
					val mut a = {peer: &mut c, data: {n: 1}}
					return &a
				}
			`,
			want:  nil,
			types: map[string]string{"f": "fn () -> {peer: {value: number}, data: {n: number}}"},
		},
		// A borrow introduced by reassigning a `var` reaches the return through the
		// flow-sensitive graph: `a = &mut b` strong-updates a to a → b, clearing the parameter
		// seed that recorded no edge.
		"ReturnVarReassignedToBorrow": {
			src: `
				fn f(seed: &mut {value: number}) {
					var a = seed
					val mut b = {value: 0}
					a = &mut b
					return a
				}
			`,
			want:  nil,
			types: map[string]string{"f": "fn <'a>(seed: &'a mut {value: number}) -> &'a mut {value: number}"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
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
			types: map[string]string{"f": "fn (p: mut {peer: &mut {value: number}}) -> undefined"},
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
			types: map[string]string{"f": "fn (p: mut {peer: &mut {value: number}}, q: &mut {value: number}) -> undefined"},
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
			types: map[string]string{"f": "fn (p: mut {node: {peer: &mut {value: number}}}) -> undefined"},
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
				"read": "fn (x: &mut {value: number}) -> undefined",
				"f":    "fn () -> undefined",
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
				"store": "fn (x: {value: number}) -> undefined",
				"f":     "fn () -> undefined",
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
				"store": "fn (y: {value: number}) -> undefined",
				"f":     "fn () -> undefined",
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
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
// Two behaviours shape the returned types here. Return borrow-stripping rewrites a returned
// `&`/`&mut` of a function-local reached exactly once into an owned field, so a tree-shaped
// component move returns an owned type while a diamond or cyclic graph keeps its borrows. The
// borrow-field owned-mutable upgrade makes `&mut` graph carriers constructible, so an
// all-`&mut` diamond is a component move rather than a construction error.
func TestConnectedComponentMove(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// The canonical case: the owned binding a holds `&mut b`, and nothing outside the
		// {a, b} component references either node, so returning a moves the whole component
		// out. No escape, and a and b are both consumed. b is reached once, so borrow-stripping
		// rewrites the return to the owned `{peer: {value: number}}`.
		"ReturnSelfContainedComponent": {
			src: `
				fn build() {
					val mut b = {value: 2}
					val a = {peer: &mut b}
					return a
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> {peer: {value: number}}"},
		},
		// A component with two borrowed locals moves as a unit just the same: both b and c
		// are reachable only through a, so returning a co-moves all three. b and c are each
		// reached once, so borrow-stripping rewrites the return to the owned
		// `{p: {x: number}, q: {x: number}}`.
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
			types: map[string]string{"build": "fn () -> {p: {x: number}, q: {x: number}}"},
		},
		// The owned carrier may be a fresh literal with no intervening binding: the returned
		// object owns the borrow of b, and b is reachable only through it. b is reached once,
		// so borrow-stripping rewrites the return to the owned `{peer: {value: number}}`.
		"ReturnInlineLiteralComponent": {
			src: `
				fn build() {
					val mut b = {value: 2}
					return {peer: &mut b}
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> {peer: {value: number}}"},
		},
		// A whole-binding move carries the graph forward: `val a2 = a` moves a, borrow and
		// all, into a2. The dead a is not a live external reference to b, so returning a2
		// still moves the {a2, b} component out. b is reached once, so borrow-stripping
		// rewrites the return to the owned `{peer: {value: number}}`.
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
			types: map[string]string{"build": "fn () -> {peer: {value: number}}"},
		},
		// An acyclic shared graph moves out the same way: a holds `&b`, and b is reachable
		// only through a. b is reached once, so borrow-stripping rewrites the return to the
		// owned `{peer: {value: number}}`. Stripping covers shared `&` borrows as well as
		// `&mut`, and a shared borrow leaves the owned form immutable.
		"ReturnSharedComponent": {
			src: `
				fn build() {
					val mut b = {value: 2}
					val a = {peer: &b}
					return a
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> {peer: {value: number}}"},
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
				"store": "fn (x: {peer: &mut {value: number}}) -> undefined",
				"f":     "fn () -> undefined",
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
				"store": "fn (x: {peer: &mut {value: number}}) -> undefined",
				"f":     "fn () -> undefined",
			},
		},
		// An escape through a consuming call inside a return is a component move at the
		// argument: the literal owning `&mut b` moves into id, b reachable only through it.
		// No escape is reported at either the argument or the enclosing return. f's return is
		// the call result `id(…)`, which hides its borrow behind the call boundary, so
		// borrow-stripping leaves f's return borrowed rather than rewriting it to owned.
		// Stripping through a consuming call needs lifetime machinery that maps the callee's
		// returned borrow back to the moved local, deferred past this work.
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
		// Every node is reached once, so borrow-stripping rewrites the return to the owned
		// `{peer: {peer: {peer: {value: 4}}}}`.
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
			types: map[string]string{"build": "fn () -> {peer: {peer: {peer: {value: 4}}}}"},
		},
		// A diamond shares a node: a borrows b and c, and both b and c borrow d, so d is
		// reachable through two paths. The component is still {a, b, c, d} and moves out as a
		// unit; reaching d twice is collapsed by the reachability walk's seen set. d is reached
		// through two paths, so borrow-stripping KEEPS the borrows — an owned tree cannot
		// express the shared node — and this return type stays borrowed. A node reached more
		// than once is what bounds stripping to tree-shaped graphs.
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
		// The mutable analog of the shared diamond moves as a unit. The borrow-field
		// owned-mutable upgrade makes `val mut b = {peer: &mut d}` owned-mutable, so `&mut b`
		// and `&mut c` build the carrier a. The component reachable from a is {a, b, c, d}, and
		// nothing outside it references a node, so returning a co-moves the whole graph. d is
		// reached through two mutable paths, so borrow-stripping keeps the borrows and the
		// moved type stays `{l: &mut {peer: &mut {x: number}}, r: &mut {peer: &mut {x:
		// number}}}`. The two mutable paths to d are both internal to the moved component, the
		// same aliasing MutableAliasRejectsMove rejects when one path is left live outside it.
		"MutableDiamondMovesAsUnit": {
			src: `
				fn build() {
					val mut d = {x: 0}
					val mut b = {peer: &mut d}
					val mut c = {peer: &mut d}
					val a = {l: &mut b, r: &mut c}
					return a
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> {l: &mut {peer: &mut {x: number}}, r: &mut {peer: &mut {x: number}}}"},
		},
		// A node mutably aliased outside the moved component blocks the move: b and c each hold
		// `&mut d`, and c is read after the store, so it is a live second mutable path to d when
		// storing b tries to move it out. This is the same external-reference rejection as the
		// shared case, and it is what keeps a mutable same-graph alias from being co-moved out
		// from under a live writer. The escape is demonstrated at a store site, not a return,
		// because at a return every local is dead — see DeadExternalRefAllowsMove — so a live
		// external alias only exists where it is used after the escape.
		"MutableAliasRejectsMove": {
			src: `
				fn store(x: {peer: &mut {x: number}}) {}
				fn f() {
					val mut d = {x: 0}
					val b = {peer: &mut d}
					val c = {peer: &mut d}
					store(b)
					val y = c
				}
			`,
			want: []string{"7:12-7:13: borrowed value 'd' does not live long enough to escape the function"},
			types: map[string]string{
				"store": "fn (x: {peer: &mut {x: number}}) -> undefined",
				"f":     "fn () -> undefined",
			},
		},
		// Two mutable aliases of one node are fine when both live inside the moved component.
		// b and c each hold `&mut d`, and a holds shared borrows of both, so the component
		// reachable from a is {a, b, c, d} and both `&mut d` paths are internal to it. The
		// model permits several `&mut` borrows live at once, so this is a legal value, and
		// returning a re-anchors the whole graph — both internal mutable paths included — with
		// no external observer. This is the mirror of MutableAliasRejectsMove: there the second
		// `&mut d` was left outside the component and rejected; here a owns both, so it is not.
		// d is reached through two paths, so borrow-stripping keeps the borrows and this return
		// type stays borrowed.
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
				"store": "fn (x: {l: &{peer: &mut {x: number}}, r: &{peer: &mut {x: number}}}) -> undefined",
				"f":     "fn () -> undefined",
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
				"store": "fn (x: {peer: &{peer: &{peer: &{value: number}}}}) -> undefined",
				"f":     "fn () -> undefined",
			},
		},
		// A second borrow of a node that is dead by the escape point does not pin the
		// component: keep holds `&b`, but it is never read after `return a`, and at a return
		// every local is dead, so keep observes nothing once b is co-moved. The move applies
		// and no escape is reported. Self-containment is about a LIVE external reference, so a
		// stray unused borrow before a return is not one. The returned carrier a reaches b once,
		// so borrow-stripping rewrites its return to the owned `{peer: {value: number}}`. keep's
		// separate dead borrow is not part of a's reachable tree.
		"DeadExternalRefAllowsMove": {
			src: `
				fn build() {
					val mut b = {value: 2}
					val a = {peer: &b}
					val keep = &b
					return a
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> {peer: {value: number}}"},
		},
		// A node reachable from a LIVE binding outside the component is not self-contained: keep
		// holds `&b` and is read after the store, so it is a live external reference to b when
		// storing a tries to move the {a, b} component out. The move does not apply and the
		// ordinary escape stands. Contrast DeadExternalRefAllowsMove, where the same borrow was
		// dead at a return.
		"LiveExternalRefRejectsMove": {
			src: `
				fn store(x: {peer: &{value: number}}) {}
				fn f() {
					val mut b = {value: 2}
					val a = {peer: &b}
					val keep = &b
					store(a)
					val y = keep
				}
			`,
			want: []string{"7:12-7:13: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{
				"store": "fn (x: {peer: &{value: number}}) -> undefined",
				"f":     "fn () -> undefined",
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
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
			c := newTestChecker()
			c.fn = &funcCtx{
				paramVarIDs: set.NewSet[liveness.VarID](),
			}
			e := &ast.IdentExpr{Name: "root", VarID: int(tc.root)}
			got := c.escapingLocalsOf(e, tc.edges).ToSlice()
			require.ElementsMatch(t, tc.want, got)
		})
	}
}
