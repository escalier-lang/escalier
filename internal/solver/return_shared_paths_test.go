package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSharedReturnPaths covers a returned value that reaches one local more than once.
//
// Two paths to one object hand the caller two views of it, and that is a hazard when one view
// writes and the other reads. Two paths of the same mutability are fine. Two readers see one
// unchanging value, and two writers are what Rule 3 allows.
//
// A returned literal is counted from its own elements rather than from its type, because an
// element that is a field read records its type as a variable the evaluator settles after this
// pass runs. `a.peer` would otherwise look like no borrow at all.
func TestSharedReturnPaths(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// Two borrows of disjoint fields of one local reach data neither can see the other
		// write, so the pair is not a hazard. The carrier is a binding, which pairs the return
		// type's positions against the graph's edges, and a tuple, whose elements all sit at
		// the carrier's own path. The two edges are told apart by the field they reach inside b
		// rather than by where they sit in the carrier.
		"DisjointFieldsInATupleOk": {
			src: `
				fn build() {
					val mut b = {x: {n: 1}, y: {n: 2}}
					val t = [&mut b.x, &mut b.y]
					return t
				}
			`,
			want: nil,
		},
		// The same field reached by a writer and a reader is the hazard the disjoint cases are
		// measured against. Both paths reach b.x, and the write through p is visible through q,
		// whose type says b.x does not change.
		"OneFieldWrittenAndReadReported": {
			src: `
				fn build() {
					val mut b = {x: {n: 1}, y: {n: 2}}
					val t = {p: &mut b.x, q: &b.x}
					return t
				}
			`,
			want: []string{"5:13-5:14: returned value reaches 'b' through a mutable path and an immutable one"},
		},
		// Rule 3: two mutable references to one value are allowed while their types match. Both
		// paths reach b.x as `&mut {n: number}`, so neither can observe a type the other breaks,
		// and the GC'd target leaves no reference for the second writer to dangle.
		"OneFieldTwiceMutableOk": {
			src: `
				fn build() {
					val mut b = {x: {n: 1}, y: {n: 2}}
					val t = {p: &mut b.x, q: &mut b.x}
					return t
				}
			`,
			want: nil,
		},
		// A borrow of the whole binding contains a borrow of any field of it, so the two paths
		// overlap even though their field paths differ. The write through p reaches b.y, which
		// q reads.
		"WholeBindingContainsAFieldReported": {
			src: `
				fn build() {
					val mut b = {x: {n: 1}, y: {n: 2}}
					val t = {p: &mut b, q: &b.y}
					return t
				}
			`,
			want: []string{"5:13-5:14: returned value reaches 'b' through a mutable path and an immutable one"},
		},
		// The second repro in #1263. a.peer and `&mut b` both lead to b. The literal walk reads
		// an element that is not a written `&mut` as a path that does not write, so a.peer
		// counts as a reader here even though a holds a mutable borrow of b. Judged on what
		// a.peer actually is, this pair is two writers and Rule 3 allows it.
		"TupleReachesOneLocalTwice": {
			src: `
				fn build() -> [&mut {value: number}, &mut {value: number}] {
					val mut b = {value: 2}
					val mut a = {peer: &mut b}
					return [a.peer, &mut b]
				}
			`,
			want: []string{"5:13-5:29: returned value reaches 'b' through a mutable path and an immutable one"},
		},
		// An object literal reaches the same pair through named fields.
		"ObjectReachesOneLocalTwice": {
			src: `
				fn build() -> {p: &mut {value: number}, q: &mut {value: number}} {
					val mut b = {value: 2}
					val mut a = {peer: &mut b}
					return {p: a.peer, q: &mut b}
				}
			`,
			want: []string{"5:13-5:35: returned value reaches 'b' through a mutable path and an immutable one"},
		},
		// Two readers see the same value, so nothing can disagree.
		"TwoSharedPathsOk": {
			src: `
				fn build() -> [&{value: number}, &{value: number}] {
					val mut b = {value: 2}
					val mut a = {peer: &b}
					return [a.peer, &b]
				}
			`,
			want: nil,
		},
		// Separate locals share no data, so two mutable paths are two objects.
		"DisjointLocalsOk": {
			src: `
				fn build() -> [&mut {value: number}, &mut {value: number}] {
					val mut b = {value: 2}
					val mut c = {value: 3}
					val mut a = {peer: &mut b}
					return [a.peer, &mut c]
				}
			`,
			want: nil,
		},
		// One path to b is the case #1264 accepts and re-types, so it must stay quiet here.
		// The annotation names the re-typed shape: the component move makes the return the
		// sole owner of b, so the field is an owned `{value: number}` rather than a borrow.
		"SinglePathOk": {
			src: `
				fn build() -> {peer: {value: number}} {
					val mut b = {value: 2}
					val mut a = {peer: &mut b}
					return a
				}
			`,
			want: nil,
		},
		// Only one arm of an `if` runs, so the two borrows of b are never live together. The
		// carrier is neither a place nor a literal, so no count is taken at all.
		"BorrowsOnAlternativeArmsOk": {
			src: `
				fn build() -> &mut {value: number} {
					val mut b = {value: 0}
					return if true { &mut b } else { &mut b }
				}
			`,
			want: nil,
		},
		// The first repro in #1263, where a call's store effect is what aliases the two paths
		// rather than an initializer. The store leaves a.peer reaching b, so the tuple hands out
		// the same pair. The store-effect loan also sees the read of b here; the return's
		// diagnostic subsumes it, so one mistake yields one message.
		"StoreAliasedPathsReportOnce": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
					item: &'a mut {value: number},
				) -> undefined
				fn build(p: mut {value: number}, q: mut {value: number}) -> [&mut {value: number}, &mut {value: number}] {
					val mut b = {value: 2}
					val mut a = {peer: &mut p, spare: &mut q}
					store(&mut a, &mut b)
					return [a.peer, &mut b]
				}
			`,
			want: []string{"10:13-10:29: returned value reaches 'b' through a mutable path and an immutable one"},
		},
		// Two DISJOINT fields of one local are two objects, so neither path can observe the
		// other's write. This is the returned-literal route, which compares the places its
		// elements name.
		"DisjointFieldsInAReturnedLiteralOk": {
			src: `
				fn build() -> [&mut {v: number}, &mut {v: number}] {
					val mut b = {x: {v: 1}, y: {v: 2}}
					return [&mut b.x, &mut b.y]
				}
			`,
			want: nil,
		},
		// The subsumption covers only reads INSIDE the reported return. This read of b.v sits
		// earlier in the body and conflicts with a's borrow on its own, so it keeps its
		// diagnostic while the return keeps its own.
		"AReadOutsideTheReturnKeepsItsDiagnostic": {
			src: `
				declare fn write(a: &mut {v: number}) -> undefined
				fn g() -> [&mut {v: number}, &{v: number}] {
					val mut b = {v: 1}
					var a = &mut b
					val n = b.v
					write(a)
					return [&mut b, &b]
				}
			`,
			want: []string{
				"8:13-8:25: returned value reaches 'b' through a mutable path and an immutable one",
				"8:19-8:20: use of partially moved value 'b'; field 'b.v' was moved out",
				"8:23-8:24: use of partially moved value 'b'; field 'b.v' was moved out",
				"6:14-6:17: cannot use 'b.v' while it is borrowed as mutable",
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
