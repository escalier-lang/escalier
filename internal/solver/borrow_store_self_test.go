package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// selfStoreDecls is the container the receiver-store cases call into. `put` spells the store
// by sharing the class's 'a between the receiver's type and the argument, which is how a
// container ties an element borrow to the container it lands in. `look` is the same shape
// with a shared receiver, the control for a method that takes no write.
const selfStoreDecls = `
	class Holder<'a> {
		peer: &'a mut {value: number},
		put(mut self, p: &'a mut {value: number}) -> undefined { self.peer = p },
		look(self, p: &'a mut {value: number}) -> undefined { },
	}
`

const selfStoreHolderType = "<'a> {new (peer: &'a mut {value: number}) -> Holder<'a>}"

// TestSelfReceiverStoreEdge covers the store a method declares into its own receiver, the
// `a.peers.push(&mut b)` shape. memberValue strips the receiver off the signature it hands
// the call site, so the recorder reads the declared receiver from the side table memberValue
// fills and roots the edge at the receiver expression.
func TestSelfReceiverStoreEdge(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// The method call is the only thing that aliases h to b, and reading the field back
		// carries that borrow out of the frame.
		"StoreIntoALocalReceiverEscapes": {
			src: selfStoreDecls + `
				fn build(p: mut {value: number}) -> &mut {value: number} {
					val mut b = {value: 2}
					val mut h = Holder(&mut p)
					h.put(&mut b)
					return h.peer
				}
			`,
			want:  []string{"12:13-12:19: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"build": "fn (p: mut {value: number}) -> &mut {value: number}"},
		},
		// Dropping the call isolates its effect: with no edge from h to b, the same read
		// reports nothing.
		"NoCallLeavesTheReceiverAlone": {
			src: selfStoreDecls + `
				fn build(p: mut {value: number}) -> &mut {value: number} {
					val mut b = {value: 2}
					val mut h = Holder(&mut p)
					return h.peer
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn (p: mut {value: number}) -> &mut {value: number}"},
		},
		// A receiver the caller owns outlives the frame, so a borrow of a local written into
		// it escapes at once rather than recording an edge, the same split a field store
		// makes between a local and a parameter receiver.
		"StoreIntoAParameterReceiverEscapes": {
			src: selfStoreDecls + `
				fn build<'x>(h: mut Holder<'x>) -> undefined {
					val mut b = {value: 2}
					h.put(&mut b)
				}
			`,
			want:  []string{"10:12-10:18: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"build": "fn <'a>(h: mut Holder<'a>) -> undefined"},
		},
		// A shared receiver takes no write, so a lifetime it shares with the argument
		// declares no store and the call records nothing.
		"SharedReceiverRecordsNoEdge": {
			src: selfStoreDecls + `
				fn build(p: mut {value: number}) -> &mut {value: number} {
					val mut b = {value: 2}
					val mut h = Holder(&mut p)
					h.look(&mut b)
					return h.peer
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn (p: mut {value: number}) -> &mut {value: number}"},
		},
		// The bracket form of a member access reaches the same method, so it records the same
		// edge. resolveIndexPath routes a constant string key through the member lookup that
		// records the receiver.
		"BracketFormRecordsTheSameEdge": {
			src: selfStoreDecls + `
				fn build(p: mut {value: number}) -> &mut {value: number} {
					val mut b = {value: 2}
					val mut h = Holder(&mut p)
					h["put"](&mut b)
					return h.peer
				}
			`,
			want:  []string{"12:13-12:19: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"build": "fn (p: mut {value: number}) -> &mut {value: number}"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(errs))
			require.Equal(t, selfStoreHolderType, values["Holder"])
			for name, want := range tc.types {
				require.Equal(t, want, values[name], "value binding %s", name)
			}
		})
	}
}

// TestSelfReceiverIsAStoreSource covers the receiver on the other side of a store. A
// signature sharing the receiver's lifetime with a `&mut` parameter's referent says the
// callee may write the receiver's data out into that parameter, so the receiver is the
// argument being stored and the parameter is the target.
//
// The shared lifetime sits inside the receiver's type rather than being the receiver's own
// borrow lifetime, so what lands in the target is whatever the receiver holds. Against a
// local target that is an edge from the target to the receiver, which a later flow-out
// follows. Against a PARAMETER target it becomes an escape site for the post-pass, whose
// answer is what the receiver's borrow edges reach — nothing at all for a class instance
// today, since a borrow passed to a constructor is not recorded as an edge to its result.
func TestSelfReceiverIsAStoreSource(t *testing.T) {
	const decls = `
		class Holder<'a> {
			peer: &'a mut {value: number},
			drain(self, out: &mut {slot: &'a mut {value: number}}) -> undefined { out.slot = self.peer },
		}
	`
	tests := map[string]struct {
		src  string
		want []string
	}{
		// The call is the only thing that aliases o to h, so returning o.slot follows that
		// edge and reports the receiver, whose data the field now exposes.
		"DrainIntoALocalRecordsAnEdge": {
			src: decls + `
				fn build(p: mut {value: number}) -> &mut {value: number} {
					val mut b = {value: 2}
					val mut h = Holder(&mut b)
					val mut o = {slot: &mut p}
					h.drain(&mut o)
					return o.slot
				}
			`,
			want: []string{"12:13-12:19: borrowed value 'h' does not live long enough to escape the function"},
		},
		// Dropping the call isolates its effect: with no edge from o to h, the same return
		// reports nothing.
		"NoCallLeavesTheTargetAlone": {
			src: decls + `
				fn build(p: mut {value: number}) -> &mut {value: number} {
					val mut b = {value: 2}
					val mut h = Holder(&mut b)
					val mut o = {slot: &mut p}
					return o.slot
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

// TestIndirectStoreIntoParameterEscapes covers what escapes when the shared lifetime sits
// inside the stored argument rather than being the argument's own borrow lifetime. What
// lands in the caller's object is what the argument HOLDS, so the locals its borrow edges
// reach dangle and its own root does not.
//
// The signature here is a plain function rather than a method, since a class instance carries
// no borrow edges: a borrow passed to a constructor is not recorded as an edge to its result.
// The rule is the same either way, and TestSelfReceiverIsAStoreSource covers the method form
// against a local target, where the edge is what a later flow-out follows.
func TestIndirectStoreIntoParameterEscapes(t *testing.T) {
	const decl = `
		declare fn drain<'a>(
			src: &mut {held: &'a mut {value: number}},
			out: &mut {slot: &'a mut {value: number}},
		) -> undefined
	`
	tests := map[string]struct {
		src  string
		want []string
	}{
		// s holds a borrow of the local b, so draining s into the caller's out leaves b
		// dangling there.
		"HoldingALocalEscapesIt": {
			src: decl + `
				fn build(o: &mut {slot: &mut {value: number}}) -> undefined {
					val mut b = {value: 2}
					val mut s = {held: &mut b}
					drain(&mut s, o)
				}
			`,
			want: []string{"10:12-10:18: borrowed value 'b' does not live long enough to escape the function"},
		},
		// An argument that builds its carrier inline names no place and so has no borrow
		// edges. What it holds is the borrows written into the carrier, found by the same
		// scan the escape check runs over a returned value.
		"HoldingALocalThroughAnInlineCarrierEscapesIt": {
			src: `
				declare fn drain<'a>(
					src: &{held: &'a mut {value: number}},
					out: &mut {slot: &'a mut {value: number}},
				) -> undefined
				fn build(o: &mut {slot: &mut {value: number}}) -> undefined {
					val mut b = {value: 2}
					drain(&{held: &mut b}, o)
				}
			`,
			want: []string{"8:12-8:27: borrowed value 'b' does not live long enough to escape the function"},
		},
		// s holds a borrow of a parameter, whose lifetime the caller supplied, so draining it
		// writes nothing that can dangle. Reporting s here — the argument's own root, which a
		// direct store would report — would be a false positive on sound code.
		"HoldingAParameterBorrowIsExempt": {
			src: decl + `
				fn build(o: &mut {slot: &mut {value: number}}, q: mut {value: number}) -> undefined {
					val mut s = {held: &mut q}
					drain(&mut s, o)
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
