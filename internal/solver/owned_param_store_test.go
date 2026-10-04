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

// TestStoreIntoOwnedParameter covers which parameters a store hands a local out through.
//
// Only a BORROW parameter names data the caller still holds. `&T` and `&mut T` reach an object
// the caller keeps, so a borrow of a local written into one reaches the caller. The store takes
// a loan of the local that the parameter holds while the body reads it. An owned parameter,
// `mut T` or plain `T`, is MOVED into the frame: the
// caller gave up every handle at the call, and the value dies with the frame, so nothing
// written into it outlives anything.
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
		// A borrow parameter's referent belongs to the caller and outlives the call, so the
		// caller reaches b through it. The runtime is garbage collected, so b stays alive, and
		// nothing in the frame reaches b after the store. The stored borrow is the only path to
		// b once f returns, so the store is accepted.
		"StoreIntoABorrowParameterOk": {
			src: ownedParamDecls + `
				fn f(p: &mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined {
					val mut b = {value: 2}
					store(&mut p, &mut b)
				}
			`,
			want: nil,
		},
		// f never reads p after the store, so the caller is the next to see b. Moving b into a
		// binding that dies with the frame changes nothing the caller can observe.
		"MovingAfterAStoreIntoABorrowParameterOk": {
			src: ownedParamDecls + `
				fn f(p: &mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined {
					val mut b = {value: 2}
					store(&mut p, &mut b)
					val y = b
				}
			`,
			want: nil,
		},
		// The contrast. p holds the loan the store takes while f reads p, so moving b out
		// before the read of p conflicts with it.
		"MovingAfterAStoreIntoABorrowParameterConflicts": {
			src: ownedParamDecls + `
				fn f(p: &mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined {
					val mut b = {value: 2}
					store(&mut p, &mut b)
					val y = b
					val z = p
				}
			`,
			want: []string{"12:14-12:15: cannot move 'b' while it is borrowed"},
		},
		// An immutable borrow of b taken after the same store disagrees with the mutable one
		// the caller holds, so it is reported against the same loan.
		"BorrowingImmutablyAfterAStoreIntoABorrowParameterConflicts": {
			src: ownedParamDecls + `
				fn f(p: &mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined {
					val mut b = {value: 2}
					store(&mut p, &mut b)
					val r = &b
					val z = p
				}
			`,
			want: []string{"12:14-12:16: cannot borrow 'b' as immutable while it is borrowed as mutable"},
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
		// p.peer reaches b, and `out` is a borrow parameter, so storing into it takes a loan of
		// b. Moving b afterwards conflicts with that loan.
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
					val z = out
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
