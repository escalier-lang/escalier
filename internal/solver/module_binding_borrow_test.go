package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestModuleBindingMovesAndBorrows covers moves and borrows of a binding declared at a
// module's top level. A function's body tracks each module-level binding it reaches, and a
// call of a module function takes a loan of each module-level binding that function reaches.
func TestModuleBindingMovesAndBorrows(t *testing.T) {
	const decls = `
		declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined
		declare fn take(x: {v: number}) -> undefined
	`
	tests := map[string]struct {
		src  string
		want []string
	}{
		"MovedTwiceInOneFunction": {
			src: decls + `
				val x = {v: 1}
				fn test() {
					take(x)
					take(x)
				}
			`,
			want: []string{"8:11-8:12: use of moved value 'x'"},
		},
		"CallReadsAfterTheMove": {
			src: decls + `
				val x = {v: 1}
				fn peek() { readRead(&x, &x) }
				fn test() {
					take(x)
					peek()
				}
			`,
			want: []string{"9:6-9:10: this call may run 'peek', which reads 'x' after it was moved"},
		},
		"CallWritesWhileBorrowed": {
			src: decls + `
				val mut x = {v: 1}
				fn bump() { x.v = 5 }
				fn test() {
					val a = &x
					bump()
					readRead(a, a)
				}
			`,
			want: []string{"9:6-9:10: this call may run 'bump', which writes 'x' while it is borrowed as immutable"},
		},
		"CallReadsWhileBorrowed": {
			src: decls + `
				val mut x = {v: 1}
				fn look() { readRead(&x, &x) }
				fn test() {
					val a = &x
					look()
					readRead(a, a)
				}
			`,
			want: nil,
		},
		"CallWritesThroughAnotherModuleFunction": {
			src: decls + `
				val mut x = {v: 1}
				fn bump() { x.v = 5 }
				fn bumpTwice() {
					bump()
					bump()
				}
				fn test() {
					val a = &x
					bumpTwice()
					readRead(a, a)
				}
			`,
			want: []string{"13:6-13:15: this call may run 'bumpTwice', which writes 'x' while it is borrowed as immutable"},
		},
		"ParameterShadowsAModuleBindingACalleeWrites": {
			src: decls + `
				val mut x = {v: 1}
				fn bump() { x.v = 5 }
				fn test(x: {v: number}) {
					val a = &x
					bump()
					readRead(a, a)
				}
			`,
			want: nil,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}
