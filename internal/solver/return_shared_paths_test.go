package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSharedReturnPaths covers a returned value that reaches one local more than once.
//
// Two paths to one object hand the caller two views of it, and that is a hazard as soon as a
// write can go through either. Two SHARED paths are fine, since two readers see the same value.
//
// A returned literal is counted from its own elements rather than from its type, because an
// element that is a field read records its type as a variable the evaluator settles after this
// pass runs. `a.peer` would otherwise look like no borrow at all.
func TestSharedReturnPaths(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// The second repro in #1263. a.peer and `&mut b` both lead to b, so the tuple hands out
		// two mutable handles to one object.
		"TupleReachesOneLocalTwice": {
			src: `
				fn build() -> [&mut {value: number}, &mut {value: number}] {
					val mut b = {value: 2}
					val mut a = {peer: &mut b}
					return [a.peer, &mut b]
				}
			`,
			want: []string{"5:13-5:29: returned value reaches 'b' through two paths while one of them can write"},
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
			want: []string{"5:13-5:35: returned value reaches 'b' through two paths while one of them can write"},
		},
		// Both elements written as borrows outright, with no carrier in between.
		"TwoBorrowsOfOneLocal": {
			src: `
				fn build() -> [&mut {value: number}, &mut {value: number}] {
					val mut b = {value: 2}
					return [&mut b, &mut b]
				}
			`,
			want: []string{"4:13-4:29: returned value reaches 'b' through two paths while one of them can write"},
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
		"SinglePathOk": {
			src: `
				fn build() -> {peer: &mut {value: number}} {
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
			want: []string{"10:13-10:29: returned value reaches 'b' through two paths while one of them can write"},
		},
		// Two DISJOINT fields of one local are two objects, so neither path can observe the
		// other's write. The count keeps the field path for exactly this.
		"DisjointFieldsOfOneLocalOk": {
			src: `
				fn build() -> [&mut {v: number}, &mut {v: number}] {
					val mut b = {x: {v: 1}, y: {v: 2}}
					return [&mut b.x, &mut b.y]
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
