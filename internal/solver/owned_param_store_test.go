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

// TestStoreIntoOwnedParameter covers which parameters a store can escape into.
//
// Only a BORROW parameter names data the caller still holds. `&T` and `&mut T` reach an object
// the caller keeps, so a borrow of a local written into one dangles once the frame goes. An
// owned parameter, `mut T` or plain `T`, is MOVED into the frame: the caller gave up every
// handle at the call, and the value dies with the frame, so nothing written into it outlives
// anything.
//
// What remains after a store into an owned parameter is an aliasing question, not a lifetime
// one, and the borrow-exclusivity check is what answers it. The last three cases pin that
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
		// The contrast. A borrow parameter's referent belongs to the caller and outlives the
		// call, so the local written into it would dangle there.
		"StoreIntoABorrowParameterEscapes": {
			src: ownedParamDecls + `
				fn f(p: &mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined {
					val mut b = {value: 2}
					store(&mut p, &mut b)
				}
			`,
			want: []string{"11:20-11:26: borrowed value 'b' does not live long enough to escape the function"},
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
		// Reading p after the store makes both paths live at once, which is an aliasing
		// hazard rather than a lifetime one. The exclusivity check reports it.
		"ReadingAnOwnedParameterAfterAStoreConflicts": {
			src: ownedParamDecls + `
				fn f(p: mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined {
					val mut b = {value: 2}
					store(&mut p, &mut b)
					val y = b
					touch(&mut p)
				}
			`,
			want: []string{"12:14-12:15: cannot use 'b' while it is borrowed as mutable"},
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
		// p.peer reaches b, and out is a borrow parameter, so storing into it escapes.
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
				}
			`,
			want: []string{"9:17-9:23: borrowed value 'b' does not live long enough to escape the function"},
		},
		// A field store creates the same borrow a call's store effect does, so reading the stored
		// local while the receiver is still read reports the same conflict.
		"ReadingAfterAFieldStoreConflicts": {
			src: `
				declare fn touch<'d>(x: &'d mut {peer: &mut {value: number}}) -> undefined
				fn f(p: mut {peer: &mut {value: number}}) -> undefined {
					val mut b = {value: 0}
					p.peer = &mut b
					val y = b
					touch(&mut p)
				}
			`,
			want: []string{"6:14-6:15: cannot use 'b' while it is borrowed as mutable"},
		},
		// Nothing reads the receiver after the field store, so its borrow of b is dead and b is
		// reachable one way again.
		"FieldStoreWithADeadReceiverOk": {
			src: `
				fn f(p: mut {peer: &mut {value: number}}) -> undefined {
					val mut b = {value: 0}
					p.peer = &mut b
					val y = b
				}
			`,
			want: nil,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}
