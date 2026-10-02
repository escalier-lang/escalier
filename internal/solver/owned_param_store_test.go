package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ownedParamDecls declares a callee that writes its second argument into its first, plus the
// helpers each case reaches the stored data back through.
const ownedParamDecls = `
	declare fn store<'a, 'b, 'c>(
		target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
		item: &'a mut {value: number},
	) -> undefined
	declare fn takeOwned(x: mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined
	declare fn touch<'d>(x: &'d mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined
`

// TestStoreIntoOwnedParameter covers which parameters a store reaches the caller through.
//
// Only a BORROW parameter names data the caller still holds. `&T` and `&mut T` reach an object
// the caller keeps, so a borrow of a local written into one hands the caller a path to the
// local. Under GC that path keeps the local alive, so the store is accepted, and the parameter
// holds a loan of the local for the rest of the body. An owned parameter, `mut T` or plain `T`,
// is MOVED into the frame. The caller gave up every handle at the call, and the value dies with
// the frame.
//
// What remains after a store into an owned parameter is an aliasing question, not a lifetime
// one, and the borrow-exclusivity check is what answers it. The last three cases assert that
// division.
func TestStoreIntoOwnedParameter(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// The core of the rule. p is moved in and dies with the frame, and nothing reads it
		// after the store, so b is reachable one way and nothing escapes.
		"StoreIntoAnOwnedParameterIsNotAnEscapeOk": {
			src: ownedParamDecls + `
				fn f(p: mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined {
					val mut b = {value: 2}
					store(&mut p, &mut b)
				}
			`,
			want: nil,
		},
		// A borrow parameter's referent belongs to the caller, so the store hands the caller a
		// path to b. Under GC that path keeps b alive, and nothing else in f reaches b after the
		// store, so the store is accepted.
		"StoreIntoABorrowParameterOk": {
			src: ownedParamDecls + `
				fn f(p: &mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined {
					val mut b = {value: 2}
					store(&mut p, &mut b)
				}
			`,
			want: nil,
		},
		// The caller holds the stored borrow for the rest of f, so moving b after the store
		// reports even though f never reads p again.
		"MovingAfterAStoreIntoABorrowParameterConflicts": {
			src: ownedParamDecls + `
				fn f(p: &mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined {
					val mut b = {value: 2}
					store(&mut p, &mut b)
					val y = b
				}
			`,
			want: []string{"12:14-12:15: cannot move 'b' while it is borrowed"},
		},
		// An immutable borrow of b after the store conflicts with the mutable borrow the caller
		// holds, for the same reason.
		"ImmutableBorrowAfterAStoreIntoABorrowParameterConflicts": {
			src: ownedParamDecls + `
				declare fn readImm(x: &{value: number}) -> undefined
				fn f(p: &mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined {
					val mut b = {value: 2}
					store(&mut p, &mut b)
					readImm(&b)
				}
			`,
			want: []string{"13:14-13:16: cannot borrow 'b' as immutable while it is borrowed as mutable"},
		},
		// A field store follows the same split. Writing into an owned parameter's field writes
		// into a value that dies with the frame.
		"FieldStoreIntoAnOwnedParameterOk": {
			src: `
				fn f(p: mut {peer: &mut {value: number}}) -> undefined {
					val mut b = {value: 0}
					p.peer = &mut b
				}
			`,
			want: nil,
		},
		// Moving b out while p still reaches it through the store leaves p pointing at data the
		// new owner controls. That is an aliasing hazard rather than a lifetime one, and the
		// exclusivity check reports it.
		"MovingTheStoredLocalAfterAStoreConflicts": {
			src: ownedParamDecls + `
				fn f(p: mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined {
					val mut b = {value: 2}
					store(&mut p, &mut b)
					val y = b
					touch(&mut p)
				}
			`,
			want: []string{"12:14-12:15: cannot move 'b' while it is borrowed"},
		},
		// Moving the parameter out carries its alias with it. The component move re-anchors
		// the graph, consuming b along with p, so the frame keeps no path to either.
		"MovingAnOwnedParameterOutCarriesTheStoredLocalOk": {
			src: ownedParamDecls + `
				fn f(p: mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined {
					val mut b = {value: 2}
					store(&mut p, &mut b)
					takeOwned(p)
				}
			`,
			want: nil,
		},
		// Reading b after that move reads a value the co-move consumed, which names the real
		// mistake more precisely than an escape would.
		"ReadingTheStoredLocalAfterTheParameterMovesOut": {
			src: ownedParamDecls + `
				fn f(p: mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined {
					val mut b = {value: 2}
					store(&mut p, &mut b)
					takeOwned(p)
					val y = b
				}
			`,
			want: []string{"13:14-13:15: use of moved value 'b'"},
		},
		// An owned parameter takes borrow edges like a local, so an argument that reaches a
		// local only through those edges still carries it into a caller-owned target. Here
		// p.peer reaches b and out is a borrow parameter, so the store is accepted.
		"AnOwnedParameterCarriesItsEdgesIntoAStoreOk": {
			src: `
				declare fn store<'a, 'c>(
					target: &'c mut {slot: &'a mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				fn f(p: mut {peer: &mut {value: number}}, out: &mut {slot: &mut {value: number}}) -> undefined {
					val mut b = {value: 0}
					p.peer = &mut b
					store(out, p.peer)
				}
			`,
			want: nil,
		},
		// The store carries b into out, so out holds a loan of b for the rest of f and moving
		// b afterwards reports. Without the edges p.peer holds, out would reach nothing.
		"AnOwnedParameterCarriesItsEdgesIntoAStore": {
			src: `
				declare fn store<'a, 'c>(
					target: &'c mut {slot: &'a mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				fn f(p: mut {peer: &mut {value: number}}, out: &mut {slot: &mut {value: number}}) -> undefined {
					val mut b = {value: 0}
					p.peer = &mut b
					store(out, p.peer)
					val y = b
				}
			`,
			want: []string{"10:14-10:15: cannot move 'b' while it is borrowed"},
		},
		// A field store creates the same borrow a call's store effect does, so moving the stored
		// local while the receiver is still read reports the same conflict.
		"MovingAfterAFieldStoreConflicts": {
			src: `
				declare fn touch<'d>(x: &'d mut {peer: &mut {value: number}}) -> undefined
				fn f(p: mut {peer: &mut {value: number}}) -> undefined {
					val mut b = {value: 0}
					p.peer = &mut b
					val y = b
					touch(&mut p)
				}
			`,
			want: []string{"6:14-6:15: cannot move 'b' while it is borrowed"},
		},
		// A plain `T` parameter is owned too, and dies with the frame just as `mut T` does.
		// Owned-immutable collapses to the bare inner, so the bridge records its concrete type
		// rather than a RefType, and reading anything settled that is not a borrow as
		// caller-owned would report a store into it.
		//
		// Nothing valid can store into one, since both a field write and an `&mut` need a
		// mutable place, so what this asserts is the absence of a second diagnostic on top of the
		// mutability error that already rejects the program.
		"StoreIntoAPlainOwnedParameterAddsNoEscape": {
			src: `
				declare fn store<'a, 'c>(
					target: &'c mut {peer: &'a mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				fn f(p: {peer: &mut {value: number}}) -> undefined {
					val mut b = {value: 0}
					store(&mut p, &mut b)
				}
			`,
			want: []string{"8:12-8:18: cannot constrain immutable object <: mutable object"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}
