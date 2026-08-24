package solver

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCallStoreEdge covers the borrow edge a call records when its signature stores an
// argument-borrow into another argument. The signature spells the store by sharing one
// lifetime between the stored argument and a position inside the target's referent, so
//
//	declare fn store<'a, 'b>(target: &'b mut {peer: &'a mut B}, item: &'a mut B)
//
// says item lands at target.peer. `store(&mut a, &mut b)` then aliases a to b at [peer],
// the same edge `val a = {peer: &mut b}` records at its initializer.
//
// Each case declares its own `store`. 'a ties item to target's peer field, 'b is the spare
// field's own lifetime, and 'c is target's own borrow lifetime, so only peer takes the
// stored borrow and spare is a sibling position the store must miss.
//
// The edge is observed through the two rules that read the borrow-edge graph: the
// return-escape check, which reports a local a returned value still borrows, and the
// connected-component move, which co-moves the locals an escaping owned carrier owns. Each
// case builds its target's fields from parameter borrows, which the graph exempts, so the
// store call is the sole source of any edge to a local.
func TestCallStoreEdge(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// The call is the only thing that aliases a to b, and returning a.peer carries that
		// borrow out of the frame. Without the store edge the escape check finds no borrow
		// under [peer] and reports nothing.
		"StoredBorrowEscapes": {
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
			want: []string{"11:13-11:19: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{
				"store": "fn (target: &mut {peer: &mut {value: number}, spare: &mut {value: number}}, " +
					"item: &mut {value: number}) -> undefined",
				"build": "fn (p: mut {value: number}, q: mut {value: number}) -> &mut {value: number}",
			},
		},
		// The edge lands at [peer], so returning the disjoint [spare] field follows no edge
		// and reports nothing. This is what the store's field path buys over attributing the
		// borrow to the whole target.
		"StoredBorrowDoesNotEscapeThroughDisjointField": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined

				fn build(p: mut {value: number}, q: mut {value: number}) -> &mut {value: number} {
					val mut b = {value: 2}
					val mut a = {peer: &mut p, spare: &mut q}
					store(&mut a, &mut b)
					return a.spare
				}
			`,
			want: nil,
			types: map[string]string{
				"store": "fn (target: &mut {peer: &mut {value: number}, spare: &mut {value: number}}, " +
					"item: &mut {value: number}) -> undefined",
				"build": "fn (p: mut {value: number}, q: mut {value: number}) -> &mut {value: number}",
			},
		},
		// The target's own place prefixes the store's field path, so storing into a field of
		// the target records the edge at [inner, peer] and returning that nested field
		// follows it.
		"StoreThroughTargetFieldPlace": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined

				fn build(p: mut {value: number}, q: mut {value: number}) -> &mut {value: number} {
					val mut b = {value: 2}
					val mut a = {inner: {peer: &mut p, spare: &mut q}}
					store(&mut a.inner, &mut b)
					return a.inner.peer
				}
			`,
			want: []string{"11:13-11:25: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{
				"store": "fn (target: &mut {peer: &mut {value: number}, spare: &mut {value: number}}, " +
					"item: &mut {value: number}) -> undefined",
				"build": "fn (p: mut {value: number}, q: mut {value: number}) -> &mut {value: number}",
			},
		},
		// The edge reaches the connected-component move as well as the escape check: passing
		// a to a consuming parameter carries the stored borrow into the callee, so the move
		// co-moves b and the read after it is a use-after-move.
		"StoredBorrowIsCoMovedWithItsCarrier": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				declare fn take(x: mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined

				fn build(p: mut {value: number}, q: mut {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &mut p, spare: &mut q}
					store(&mut a, &mut b)
					take(a)
					val y = b
				}
			`,
			want: []string{"13:14-13:15: use of moved value 'b'"},
			types: map[string]string{
				"store": "fn (target: &mut {peer: &mut {value: number}, spare: &mut {value: number}}, " +
					"item: &mut {value: number}) -> undefined",
				"take":  "fn (x: mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined",
				"build": "fn (p: mut {value: number}, q: mut {value: number}) -> undefined",
			},
		},
		// Dropping the store call from the case above isolates its effect: with no edge from
		// a to b, consuming a co-moves nothing and the read of b is fine.
		"NoStoreLeavesTheCarrierAlone": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				declare fn take(x: mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined

				fn build(p: mut {value: number}, q: mut {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &mut p, spare: &mut q}
					take(a)
					val y = b
				}
			`,
			want: nil,
			types: map[string]string{
				"store": "fn (target: &mut {peer: &mut {value: number}, spare: &mut {value: number}}, " +
					"item: &mut {value: number}) -> undefined",
				"take":  "fn (x: mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined",
				"build": "fn (p: mut {value: number}, q: mut {value: number}) -> undefined",
			},
		},
		// A store whose target is a parameter escapes at once rather than recording an edge:
		// the parameter's referent belongs to the caller and outlives the frame, so a borrow
		// of a local written into it dangles. This is the call-site twin of the field store
		// `p.peer = &mut b`, which checkParamFieldStoreEscape reports the same way.
		"StoreIntoParameterTargetEscapes": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined

				fn build(p: mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined {
					val mut b = {value: 2}
					store(&mut p, &mut b)
				}
			`,
			want: []string{"9:20-9:26: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{
				"store": "fn (target: &mut {peer: &mut {value: number}, spare: &mut {value: number}}, " +
					"item: &mut {value: number}) -> undefined",
				"build": "fn (p: mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined",
			},
		},
		// An auto-borrowed place stored into a parameter escapes too. The argument names no
		// borrow expression, so the local it carries is the place's own root rather than
		// anything the escape post-pass would find by scanning the argument.
		"AutoBorrowedArgumentIntoParameterEscapes": {
			src: `
				declare fn store<'a, 'c>(
					target: &'c mut {peer: &'a {value: number}},
					item: &'a {value: number},
				) -> undefined
				fn build(p: mut {peer: &{value: number}}) -> undefined {
					val b = {value: 2}
					store(&mut p, b)
				}
			`,
			want: []string{"8:20-8:21: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{
				"store": "fn (target: &mut {peer: &{value: number}}, item: &{value: number}) -> undefined",
				"build": "fn (p: mut {peer: &{value: number}}) -> undefined",
			},
		},
		// Storing a borrow of a parameter records no edge: a parameter's lifetime is the
		// caller's and already outlives the frame, so it is not a local that can dangle.
		"StoredParamBorrowIsExempt": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined

				fn build(p: mut {value: number}, q: mut {value: number}, r: mut {value: number}) -> &mut {value: number} {
					val mut a = {peer: &mut p, spare: &mut q}
					store(&mut a, &mut r)
					return a.peer
				}
			`,
			want: nil,
			types: map[string]string{
				"store": "fn (target: &mut {peer: &mut {value: number}, spare: &mut {value: number}}, " +
					"item: &mut {value: number}) -> undefined",
				"build": "fn (p: mut {value: number}, q: mut {value: number}, r: mut {value: number}) " +
					"-> &mut {value: number}",
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

// TestCallStoreEdgePositions covers the store positions inside a target's referent that are
// not a named object field, and the argument forms the recorder reads a place off. Each
// records an edge the return-escape check then reports.
func TestCallStoreEdgePositions(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// A tuple element contributes no field segment, so a store into one lands at the
		// tuple itself and returning the whole tuple follows the edge.
		"StoreIntoTupleElement": {
			src: `
				declare fn store<'a, 'c>(
					target: &'c mut [&'a mut {value: number}],
					item: &'a mut {value: number},
				) -> undefined
				fn build(p: mut {value: number}) -> [&mut {value: number}] {
					val mut b = {value: 2}
					val mut a = [&mut p]
					store(&mut a, &mut b)
					return a
				}
			`,
			want: []string{"10:13-10:14: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{
				"store": "fn (target: &mut [&mut {value: number}], item: &mut {value: number}) -> undefined",
				"build": "fn (p: mut {value: number}) -> [&mut {value: number}]",
			},
		},
		// A class type argument is not addressable as a field either, so a store into a
		// borrowed `Box<&'a mut B>` field lands at that field and returning it follows the
		// edge. This is the shape a borrow-holding container takes.
		"StoreIntoClassTypeArgument": {
			src: `
				class Box<T> { value: T }
				declare fn store<'a, 'c, 'd>(
					target: &'c mut {box: &'d mut Box<&'a mut {value: number}>},
					item: &'a mut {value: number},
				) -> undefined
				fn build(seeded: mut Box<&mut {value: number}>) -> &mut Box<&mut {value: number}> {
					val mut b = {value: 2}
					val mut a = {box: &mut seeded}
					store(&mut a, &mut b)
					return a.box
				}
			`,
			want: []string{"11:13-11:18: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{
				"Box":   "<T> {new (value: T) -> Box<T>}",
				"store": "fn (target: &mut {box: &mut Box<&mut {value: number}>}, item: &mut {value: number}) -> undefined",
				"build": "fn <'a>(seeded: mut Box<&'a mut {value: number}>) -> &mut Box<&'a mut {value: number}>",
			},
		},
		// An alias is transparent, so a target's referent written under an alias name is
		// searched the way the object the alias names is.
		"StoreThroughAliasedReferent": {
			src: `
				type Holder<'a> = {peer: &'a mut {value: number}}
				declare fn store<'a, 'c>(target: &'c mut Holder<'a>, item: &'a mut {value: number}) -> undefined
				fn build(p: mut {value: number}) -> &mut {value: number} {
					val mut b = {value: 2}
					val mut a = {peer: &mut p}
					store(&mut a, &mut b)
					return a.peer
				}
			`,
			want: []string{"8:13-8:19: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{
				"store": "fn <'a>(target: &mut Holder<'a>, item: &mut {value: number}) -> undefined",
				"build": "fn (p: mut {value: number}) -> &mut {value: number}",
			},
		},
		// The stored argument's own outer lifetime is not the only one that counts: a lifetime
		// nested inside its referent names data the callee holds just as much, so sharing that
		// one with the target is a store too.
		"StoreOfNestedArgumentLifetime": {
			src: `
				declare fn store<'a, 'c, 'd>(
					target: &'c mut {peer: &'a mut {value: number}},
					item: &'d {inner: &'a mut {value: number}},
				) -> undefined
				fn build(p: mut {value: number}, q: mut {value: number}) -> &mut {value: number} {
					val mut b = {inner: &mut q}
					val mut a = {peer: &mut p}
					store(&mut a, &b)
					return a.peer
				}
			`,
			want: []string{"10:13-10:19: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{
				"store": "fn (target: &mut {peer: &mut {value: number}}, item: &{inner: &mut {value: number}}) -> undefined",
				"build": "fn (p: mut {value: number}, q: mut {value: number}) -> &mut {value: number}",
			},
		},
		// A union member holds a borrow the whole union exposes, so a store into one is found
		// at the union's own path. The two constrain errors are unrelated to the store: a
		// `&mut` field is invariant, so the argument's `&mut {value: number}` does not satisfy
		// a field declared as a union of borrows. They pin that limitation alongside the edge
		// the walk still records through the union.
		"StoreIntoUnionMember": {
			src: `
				declare fn store<'a, 'c>(
					target: &'c mut {peer: &'a mut {value: number} | &'a mut {other: number}},
					item: &'a mut {value: number},
				) -> undefined
				fn build(p: mut {value: number}) -> &mut {value: number} | &mut {other: number} {
					val mut b = {value: 2}
					val mut a = {peer: &mut p}
					store(&mut a, &mut b)
					return a.peer
				}
			`,
			want: []string{
				"6:29-6:35: object is missing property: value",
				"3:71-3:77: object has extra property: other",
				"10:13-10:19: borrowed value 'b' does not live long enough to escape the function",
			},
			types: map[string]string{
				"store": "fn (target: &mut {peer: &mut {other: number} | &mut {value: number}}, " +
					"item: &mut {value: number}) -> undefined",
				"build": "fn (p: mut {value: number}) -> &mut {value: number} | &mut {other: number}",
			},
		},
		// An argument that builds its carrier inline names no place, so the referents come
		// from the borrows the carrier holds rather than from the argument's own place.
		"StoreOfInlineCarrierArgument": {
			src: `
				declare fn store<'a, 'c, 'd>(
					target: &'c mut {peer: &'a mut {value: number}},
					item: &'d {inner: &'a mut {value: number}},
				) -> undefined
				fn build(p: mut {value: number}) -> &mut {value: number} {
					val mut b = {value: 2}
					val mut a = {peer: &mut p}
					store(&mut a, &{inner: &mut b})
					return a.peer
				}
			`,
			want: []string{"10:13-10:19: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{
				"store": "fn (target: &mut {peer: &mut {value: number}}, item: &{inner: &mut {value: number}}) -> undefined",
				"build": "fn (p: mut {value: number}) -> &mut {value: number}",
			},
		},
		// An index signature names no single field, so a store into one lands at the object
		// holding it, covering every key it admits rather than one.
		"StoreIntoIndexSignature": {
			src: `
				declare fn store<'a, 'c, 'd>(
					target: &'c mut {peers: &'d mut {[key: string]?: &'a mut {value: number}}},
					item: &'a mut {value: number},
				) -> undefined
				fn build(seeded: mut {[key: string]?: &mut {value: number}}) -> &mut {[key: string]?: &mut {value: number}} {
					val mut b = {value: 2}
					val mut a = {peers: &mut seeded}
					store(&mut a, &mut b)
					return a.peers
				}
			`,
			want: []string{"10:13-10:20: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{
				"store": "fn (target: &mut {peers: &mut {[key: string]?: &mut {value: number}}}, " +
					"item: &mut {value: number}) -> undefined",
				"build": "fn <'a>(seeded: mut {[key: string]?: &'a mut {value: number}}) " +
					"-> &mut {[key: string]?: &'a mut {value: number}}",
			},
		},
		// A shared-borrow parameter auto-borrows the place passed to it, so the stored
		// argument is a bare name rather than an `&` expression and the recorder reads the
		// place off the argument itself.
		"AutoBorrowedArgumentIsStored": {
			src: `
				declare fn store<'a, 'c>(
					target: &'c mut {peer: &'a {value: number}},
					item: &'a {value: number},
				) -> undefined
				fn build(p: {value: number}) -> &{value: number} {
					val b = {value: 2}
					val mut a = {peer: &p}
					store(&mut a, b)
					return a.peer
				}
			`,
			want: []string{"10:13-10:19: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{
				"store": "fn (target: &mut {peer: &{value: number}}, item: &{value: number}) -> undefined",
				"build": "fn (p: {value: number}) -> &{value: number}",
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

// TestCallStoreEdgePayloadPositions covers the three payload positions that hold a borrow
// without naming a field: an array element, a promise's resolved value, and a generator's
// yield. Each stores at the container holding it, so the store lands at the tuple slot the
// payload sits in and returning that tuple follows the edge.
//
// Every case also reports one constrain error unrelated to the store. `&mut a` over a tuple
// literal whose element is a bare name does not read as a mutable tuple, which is a
// pre-existing limitation of the deep-mut rule rather than anything the store recorder does.
// The same shape with a borrow element, TestCallStoreEdgePositions/StoreIntoTupleElement,
// reports only the escape.
func TestCallStoreEdgePayloadPositions(t *testing.T) {
	tests := map[string]struct {
		payload  string
		declared string
	}{
		"ArrayElement":   {payload: "Array<&'a mut {value: number}>", declared: "Array<&mut {value: number}>"},
		"PromiseValue":   {payload: "Promise<&'a mut {value: number}>", declared: "Promise<&mut {value: number}>"},
		"GeneratorYield": {payload: "Generator<&'a mut {value: number}, undefined, undefined>", declared: "Generator<&mut {value: number}, undefined, undefined>"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			src := fmt.Sprintf(`
				declare fn store<'a, 'c>(
					target: &'c mut [%s],
					item: &'a mut {value: number},
				) -> undefined
				fn build(seeded: %s) -> [%s] {
					val mut b = {value: 2}
					val mut a = [seeded]
					store(&mut a, &mut b)
					return a
				}
			`, tc.payload, tc.declared, tc.declared)
			_, _, errs := inferSource(t, src)
			require.Equal(t, []string{
				"9:12-9:18: cannot constrain immutable tuple <: mutable tuple",
				"10:13-10:14: borrowed value 'b' does not live long enough to escape the function",
			}, messagesWithSpan(errs))
		})
	}
}

// TestCallStoreEdgeNonStores covers the signatures that share a lifetime without declaring a
// store, so the call records nothing and the locals it borrows stay free. Each pairs with a
// case in TestCallStoreEdge that does record an edge on the same source shape.
func TestCallStoreEdgeNonStores(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// Two parameters sharing one lifetime at their outermost position declare no store:
		// the borrows share a region, and neither referent is reachable through the other.
		"SiblingBorrowsRecordNoEdge": {
			src: `
				declare fn pair<'a>(x: &'a mut {value: number}, y: &'a mut {value: number}) -> undefined
				declare fn take(x: mut {peer: &mut {value: number}}) -> undefined
				fn build(p: mut {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut c = {value: 3}
					val mut a = {peer: &mut p}
					pair(&mut b, &mut c)
					take(a)
					val y = b
				}
			`,
			want: nil,
			types: map[string]string{
				"pair":  "fn (x: &mut {value: number}, y: &mut {value: number}) -> undefined",
				"take":  "fn (x: mut {peer: &mut {value: number}}) -> undefined",
				"build": "fn (p: mut {value: number}) -> undefined",
			},
		},
		// A shared-borrow target takes no write, so a lifetime occurring inside it declares
		// no store and the call records nothing.
		"SharedBorrowTargetRecordsNoEdge": {
			src: `
				declare fn read<'a, 'b>(
					target: &'b {peer: &'a mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				fn build(p: mut {value: number}) -> &mut {value: number} {
					val mut b = {value: 2}
					val mut a = {peer: &mut p}
					read(&a, &mut b)
					return a.peer
				}
			`,
			want: nil,
			types: map[string]string{
				"read":  "fn (target: &{peer: &mut {value: number}}, item: &mut {value: number}) -> undefined",
				"build": "fn (p: mut {value: number}) -> &mut {value: number}",
			},
		},
		// An alias reference carries its lifetime arguments and its expansion carries them
		// again, at the field paths they really sit at. Only the expansion counts, so the
		// store lands at [peer] alone and returning the disjoint [spare] field is clean.
		// Counting the reference's arguments too would put a second edge at the alias's own
		// path, which every field of the target sits under.
		"AliasArgumentsDoNotWidenTheStorePath": {
			src: `
				type Holder<'a, 'b> = {peer: &'a mut {value: number}, spare: &'b mut {value: number}}
				declare fn store<'a, 'b, 'c>(
					target: &'c mut Holder<'a, 'b>,
					item: &'a mut {value: number},
				) -> undefined
				fn build(p: mut {value: number}, q: mut {value: number}) -> &mut {value: number} {
					val mut b = {value: 2}
					val mut a = {peer: &mut p, spare: &mut q}
					store(&mut a, &mut b)
					return a.spare
				}
			`,
			want: nil,
			types: map[string]string{
				"store": "fn <'a, 'b>(target: &mut Holder<'a, 'b>, item: &mut {value: number}) -> undefined",
				"build": "fn (p: mut {value: number}, q: mut {value: number}) -> &mut {value: number}",
			},
		},
		// An alias re-nested inside itself is two different types, so both levels expand and
		// the store lands at the borrow's real path, [deep, inner, inner]. Returning the
		// sibling [deep, inner, tag] follows no edge. Stopping at the first repeat of the
		// alias NAME would put the store one level short, at [deep, inner], which sits above
		// the returned field and would report a borrow that is not there.
		"RenestedAliasKeepsTheFullStorePath": {
			src: `
				type W<T> = {inner: T, tag: number}
				declare fn store<'a, 'c>(
					target: &'c mut {deep: W<W<&'a mut {value: number}>>},
					item: &'a mut {value: number},
				) -> undefined
				fn build(p: mut {value: number}) -> number {
					val mut b = {value: 2}
					val mut a = {deep: {inner: {inner: &mut p, tag: 1}, tag: 0}}
					store(&mut a, &mut b)
					return a.deep.inner.tag
				}
			`,
			want: nil,
			types: map[string]string{
				"store": "fn (target: &mut {deep: W<W<&mut {value: number}>>}, item: &mut {value: number}) -> undefined",
				"build": "fn (p: mut {value: number}) -> number",
			},
		},
		// An owned target is moved into the callee rather than written through, so a lifetime
		// occurring inside it declares no store either.
		"OwnedTargetRecordsNoEdge": {
			src: `
				declare fn keep<'a>(
					target: mut {peer: &'a mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				fn build(p: mut {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &mut p}
					keep(a, &mut b)
					val y = b
				}
			`,
			want: nil,
			types: map[string]string{
				"keep":  "fn (target: mut {peer: &mut {value: number}}, item: &mut {value: number}) -> undefined",
				"build": "fn (p: mut {value: number}) -> undefined",
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

// TestCallStoreEdgeAliasChainTerminates pins the node budget the lifetime walk runs under.
// The signature below names a chain of aliases where each body names the next one twice, so
// its field paths number 2^depth. Without the budget the walk enumerates all of them and the
// call takes minutes; with it the walk stops early, and the store at the chain's leaf is
// still found because the first path to reach it does so within budget.
func TestCallStoreEdgeAliasChainTerminates(t *testing.T) {
	const depth = 16
	var b strings.Builder
	for i := 1; i < depth; i++ {
		fmt.Fprintf(&b, "type A%d<'a> = {x: A%d<'a>, y: A%d<'a>}\n", i, i+1, i+1)
	}
	fmt.Fprintf(&b, "type A%d<'a> = {peer: &'a mut {value: number}}\n", depth)
	b.WriteString("declare fn store<'a, 'c>(target: &'c mut A1<'a>, item: &'a mut {value: number}) -> undefined\n")
	b.WriteString("fn build(t: mut A1<'static>) -> undefined {\n")
	b.WriteString("\tval mut b = {value: 2}\n")
	b.WriteString("\tstore(&mut t, &mut b)\n")
	b.WriteString("}\n")

	// Parsing stays on the test goroutine, since parseModule asserts through require and a
	// failed assertion off the test goroutine would surface as this test's timeout instead of
	// as the parse error it is. Only inference, the step the budget bounds, runs detached.
	module := parseModule(t, b.String())
	done := make(chan []SolverError, 1)
	go func() {
		_, _, errs := inferModule(module)
		done <- errs
	}()
	select {
	case errs := <-done:
		require.Equal(t, []string{
			"20:16-20:22: borrowed value 'b' does not live long enough to escape the function",
		}, messagesWithSpan(errs))
	case <-time.After(30 * time.Second):
		t.Fatal("inference did not finish: the lifetime walk did not stay within its node budget")
	}
}

// TestCallStoreEdgeCutWalkStaysSound covers what a walk that runs out of alias fuel records.
// The borrow sits deeper in the chain than the fuel reaches, so the exact field path is
// unknown; recording no store would drop the escape that borrow raises. The store lands at
// the whole target instead, which every field read through it follows.
func TestCallStoreEdgeCutWalkStaysSound(t *testing.T) {
	// One alias per level, each naming the next, with the borrow past maxAliasExpansionDepth.
	depth := maxAliasExpansionDepth * 2
	var b strings.Builder
	for i := 1; i < depth; i++ {
		fmt.Fprintf(&b, "type A%d<'a> = {x: A%d<'a>}\n", i, i+1)
	}
	fmt.Fprintf(&b, "type A%d<'a> = {peer: &'a mut {value: number}}\n", depth)
	b.WriteString("declare fn store<'a, 'c>(target: &'c mut A1<'a>, item: &'a mut {value: number}) -> undefined\n")
	b.WriteString("fn build(t: mut A1<'static>) -> undefined {\n")
	b.WriteString("\tval mut b = {value: 2}\n")
	b.WriteString("\tstore(&mut t, &mut b)\n")
	b.WriteString("}\n")

	_, _, errs := inferSource(t, b.String())
	require.Equal(t, []string{
		"20:16-20:22: borrowed value 'b' does not live long enough to escape the function",
	}, messagesWithSpan(errs))
}
