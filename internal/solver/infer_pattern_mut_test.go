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
			}`, want: []string{"5:5-5:12: cannot constrain immutable object <: mutable object"}},
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
			}`, want: []string{"5:5-5:12: cannot constrain immutable object <: mutable object"}},
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
			}`, want: []string{"6:7-6:14: cannot constrain immutable object <: mutable object"}},
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
			}`, want: []string{"3:5-3:12: cannot constrain immutable object <: mutable object"}},
		// Shared `&` borrow scrutinee: a `mut` leaf is rejected.
		"SharedBorrowMutLeafRejected": {src: `
			fn f(line: &[{x: number}, {y: number}]) {
				val [p, mut q] = line
				return p
			}`, want: []string{"3:13-3:18: cannot bind a `mut` leaf through an immutable borrow; the scrutinee must be owned or a `&mut` borrow"}},
		// Shared `&` borrow scrutinee: an unmarked leaf is a shared borrow, so a write errors.
		"SharedBorrowPlainLeafWriteErrors": {src: `
			fn f(line: &[{x: number}, {y: number}]) {
				val [p, q] = line
				q.y = 5
				return p
			}`, want: []string{"4:5-4:12: cannot constrain immutable object <: mutable object"}},
		// `&mut` borrow scrutinee: a `mut` leaf is a mutable borrow, so a write succeeds.
		"MutBorrowMutLeafWrite": {src: `
			fn f(line: &mut [{x: number}, {y: number}]) {
				val [p, mut q] = line
				q.y = 5
				return p
			}`},
		// `&mut` borrow scrutinee (Rust ergonomics): an unmarked leaf is also a mutable
		// borrow, so a write through it succeeds without the `mut` marker. Both leaves
		// are plain, so the write-through-unmarked path is exercised on its own.
		"MutBorrowPlainLeafWrite": {src: `
			fn f(line: &mut [{x: number}, {y: number}]) {
				val [p, q] = line
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
			require.Equal(t, tc.want, messagesWithSpan(errs))
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
// scrutinee is a mutable borrow bounded by the scrutinee, and a `&` scrutinee projects
// a shared borrow.
func TestDestructureMutBorrowLeafRendersBorrow(t *testing.T) {
	// A shared-borrow leaf renders cleanly, so the inferred function type pins the
	// projected `&'a {y: number}` directly: the leaf shares the scrutinee's lifetime.
	t.Run("SharedBorrow", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			fn f(line: &[{x: number}, {y: number}]) {
				val [p, q] = line
				return q
			}
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn <'a>(line: &'a [{x: number}, {y: number}]) -> &'a {y: number}", values["f"])
	})
	// A `mut` leaf routes its projection through a fresh variable so a write can widen
	// the element, which does not survive the return-join cleanly — a returned `&mut`
	// leaf renders as `T0 | {y: number}` rather than `&mut {y: number}`. So the leaf is
	// pinned against a `&mut {y: number}` return annotation instead: an owned or shared
	// `q` would fail that constraint, so acceptance confirms `q` is a `&mut` borrow. The
	// write-through behavior is covered by TestDestructureMutLeaf.
	t.Run("MutBorrow", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			fn f(line: &mut [{x: number}, {y: number}]) -> &mut {y: number} {
				val [p, mut q] = line
				return q
			}
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn <'a>(line: &'a mut [{x: number}, {y: number}]) -> &'a mut {y: number}", values["f"])
	})
}

// TestMatchBorrowedScrutineeMutLeaf covers per-leaf mutability in `match` arms whose
// scrutinee is a borrowed reference, the pattern-matching analogue of the `val`
// destructuring cases. The binding mode flows from the scrutinee's borrow into each
// arm's pattern: a `&mut` scrutinee projects mutable leaves, a `&` scrutinee projects
// shared leaves and rejects a `mut` leaf. A leaf never moves out of a borrowed
// scrutinee. The scrutinee is a borrow PARAMETER, the supported `&mut`/`&` path; a
// `&mut` borrow expression of a local owned value is a separate, unlanded path.
func TestMatchBorrowedScrutineeMutLeaf(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// `&mut` scrutinee: a `mut` leaf of a match arm is a mutable borrow, so a write
		// through it succeeds.
		"MutBorrowMutLeafWrite": {src: `
			fn f(line: &mut [{x: number}, {y: number}]) {
				match line {
					[p, mut q] => {
						q.y = 5
						0
					}
				}
			}`},
		// `&mut` scrutinee (Rust ergonomics): an unmarked leaf is also a mutable borrow,
		// so a write through it succeeds without the `mut` marker.
		"MutBorrowPlainLeafWrite": {src: `
			fn f(line: &mut [{x: number}, {y: number}]) {
				match line {
					[p, q] => {
						q.y = 5
						0
					}
				}
			}`},
		// `&mut` scrutinee, object pattern: `{a: mut m}` is a mutable borrow leaf.
		"MutBorrowObjectMutLeaf": {src: `
			fn f(pt: &mut {a: {x: number}, b: {y: number}}) {
				match pt {
					{a: mut m, b: n} => {
						m.x = 5
						0
					}
				}
			}`},
		// `&` scrutinee: a `mut` leaf of a match arm is rejected — mutable access cannot
		// be projected out of an immutable borrow.
		"SharedBorrowMutLeafRejected": {src: `
			fn f(line: &[{x: number}, {y: number}]) {
				match line {
					[p, mut q] => { 0 }
				}
			}`, want: []string{"4:10-4:15: cannot bind a `mut` leaf through an immutable borrow; the scrutinee must be owned or a `&mut` borrow"}},
		// `&` scrutinee: an unmarked leaf is a shared borrow, so a write through it errors.
		"SharedBorrowPlainLeafWriteErrors": {src: `
			fn f(line: &[{x: number}, {y: number}]) {
				match line {
					[p, q] => {
						q.y = 5
						0
					}
				}
			}`, want: []string{"5:7-5:14: cannot constrain immutable object <: mutable object"}},
		// A `mut` leaf is rejected against a `&` borrow EXPRESSION scrutinee too, not
		// only a `&` parameter.
		"SharedBorrowExprMutLeafRejected": {src: `
			fn f() {
				val line = [{x: 0}, {y: 0}]
				match (&line) {
					[p, mut q] => { 0 }
				}
			}`, want: []string{"5:10-5:15: cannot bind a `mut` leaf through an immutable borrow; the scrutinee must be owned or a `&mut` borrow"}},
		// The binding mode propagates into a nested pattern of a borrowed `match`
		// scrutinee, so a deeply nested `mut` leaf is a mutable borrow.
		"NestedMutBorrowLeaf": {src: `
			fn f(line: &mut [[{x: number}]]) {
				match line {
					[[mut a]] => {
						a.x = 5
						0
					}
				}
			}`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(errs))
		})
	}
}
