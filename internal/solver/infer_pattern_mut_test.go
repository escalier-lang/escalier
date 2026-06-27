package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Per-leaf `mut` in destructuring patterns (#793). Mutability is per leaf and written
// inside the pattern. The binding mode flows from the scrutinee's outermost borrow:
// an owned scrutinee moves each leaf out, a `&mut` scrutinee projects a mutable borrow
// of each leaf, and a `&` scrutinee projects a shared borrow where a `mut` leaf is
// rejected.

// TestDestructureMutLeaf covers the owned, shared-borrow, and mutable-borrow forms of
// per-leaf mutability across `val` destructuring, function params, and `match` arms.
// Each case asserts the exact set of error messages, empty for a well-typed program.
func TestDestructureMutLeaf(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// Owned scrutinee: a `mut` tuple leaf is owned-mutable, so a write succeeds.
		"OwnedTupleMutLeafWrite": {src: `
			fn f() {
				val line = [{x: 0}, {y: 0}]
				val [p, mut q] = line
				q.y = 5
				return p
			}`},
		// Owned scrutinee: an unmarked tuple leaf is immutable, so the same write errors.
		"OwnedTuplePlainLeafWriteErrors": {src: `
			fn f() {
				val line = [{x: 0}, {y: 0}]
				val [p, q] = line
				q.y = 5
				return p
			}`, want: []string{"cannot constrain immutable object <: mutable object"}},
		// Owned scrutinee, object key-value form: `{a: mut m}` is mutable, `{b: n}` not.
		"OwnedObjectKeyValueMutLeaf": {src: `
			fn f() {
				val pt = {a: {x: 0}, b: {y: 0}}
				val {a: mut m, b: n} = pt
				m.x = 5
				return n
			}`},
		"OwnedObjectKeyValuePlainLeafWriteErrors": {src: `
			fn f() {
				val pt = {a: {x: 0}, b: {y: 0}}
				val {a: m, b: n} = pt
				m.x = 5
				return n
			}`, want: []string{"cannot constrain immutable object <: mutable object"}},
		// Owned scrutinee, object shorthand form: `{mut x}` is mutable.
		"OwnedObjectShorthandMutLeaf": {src: `
			fn f() {
				val pt = {x: {a: 0}, y: 0}
				val {mut x, y} = pt
				x.a = 5
				return y
			}`},
		// A `mut` leaf in a `match` arm over an owned scrutinee is mutable.
		"OwnedMatchMutLeaf": {src: `
			fn f() {
				val line = [{x: 0}, {y: 0}]
				match line {
					[p, mut q] => {
						q.y = 5
						p
					}
				}
			}`},
		// An unmarked leaf in a `match` arm over an owned scrutinee is immutable.
		"OwnedMatchPlainLeafWriteErrors": {src: `
			fn f() {
				val line = [{x: 0}, {y: 0}]
				match line {
					[p, q] => {
						q.y = 5
						p
					}
				}
			}`, want: []string{"cannot constrain immutable object <: mutable object"}},
		// A destructured `mut` parameter leaf is mutable.
		"OwnedParamMutLeaf": {src: `
			fn f([a, mut b]: [{x: number}, {y: number}]) {
				b.y = 5
				return a
			}`},
		"OwnedParamPlainLeafWriteErrors": {src: `
			fn f([a, b]: [{x: number}, {y: number}]) {
				b.y = 5
				return a
			}`, want: []string{"cannot constrain immutable object <: mutable object"}},
		// Shared `&` borrow scrutinee: a `mut` leaf is rejected.
		"SharedBorrowMutLeafRejected": {src: `
			fn f(line: &[{x: number}, {y: number}]) {
				val [p, mut q] = line
				return p
			}`, want: []string{"cannot bind a `mut` leaf through an immutable borrow; the scrutinee must be owned or a `&mut` borrow"}},
		// Shared `&` borrow scrutinee: an unmarked leaf is a shared borrow, so a write errors.
		"SharedBorrowPlainLeafWriteErrors": {src: `
			fn f(line: &[{x: number}, {y: number}]) {
				val [p, q] = line
				q.y = 5
				return p
			}`, want: []string{"cannot constrain immutable object <: mutable object"}},
		// `&mut` borrow scrutinee: a `mut` leaf is a mutable borrow, so a write succeeds.
		"MutBorrowMutLeafWrite": {src: `
			fn f(line: &mut [{x: number}, {y: number}]) {
				val [p, mut q] = line
				q.y = 5
				return p
			}`},
		// `&mut` borrow scrutinee (Rust ergonomics): an unmarked leaf is also a mutable
		// borrow, so a write through it succeeds without the `mut` marker.
		"MutBorrowPlainLeafWrite": {src: `
			fn f(line: &mut [{x: number}, {y: number}]) {
				val [mut p, q] = line
				q.y = 5
				p.x = 1
				return 0
			}`},
		// The binding mode propagates into nested patterns, so a deeply nested `mut`
		// leaf of a `&mut` scrutinee is a mutable borrow.
		"NestedMutBorrowLeaf": {src: `
			fn f(line: &mut [[{x: number}]]) {
				val [[mut a]] = line
				a.x = 5
				return 0
			}`},
		// A primitive element of a borrowed scrutinee is copied, not borrowed, so the
		// leaf binds at the primitive's value type and satisfies a `number` return —
		// it is not wrapped in a `&mut number`/`&number` that would fail the lifetime
		// check.
		"MutBorrowPrimitiveLeafCopies": {src: `
			fn f(line: &mut [number, {y: number}]) -> number {
				val [a, q] = line
				return a
			}`},
		"SharedBorrowPrimitiveLeafCopies": {src: `
			fn f(line: &[number, {y: number}]) -> number {
				val [a, q] = line
				return a
			}`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, Messages(errs))
		})
	}
}

// TestDestructureMutLeafRendersThawedCell checks that an owned `mut` leaf moved out of
// a concrete scrutinee renders as a clean owned-mutable cell with widened fields, the
// destructuring analogue of the `val mut q = p` thaw.
func TestDestructureMutLeafRendersThawedCell(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f() {
			val line = [{x: 0}, {y: 0}]
			val [p, mut q] = line
			q.y = 5
			return q
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> mut {y: number}", values["f"])
}

// TestDestructureMutBorrowLeafRendersBorrow checks that a leaf projected from a `&mut`
// scrutinee renders as a mutable borrow bounded by the scrutinee, and a `&` scrutinee
// projects a shared borrow. The return annotations pin the expected leaf types.
func TestDestructureMutBorrowLeafRendersBorrow(t *testing.T) {
	t.Run("MutBorrow", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn f(line: &mut [{x: number}, {y: number}]) -> &mut {y: number} {
				val [p, mut q] = line
				return q
			}
		`)
		require.Empty(t, errs)
	})
	t.Run("SharedBorrow", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn f(line: &[{x: number}, {y: number}]) -> &{y: number} {
				val [p, q] = line
				return q
			}
		`)
		require.Empty(t, errs)
	})
}
