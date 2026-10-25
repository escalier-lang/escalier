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
		"PassedTwiceToAnOwnedParameter": {
			src: decls + `
				val x = {v: 1}
				fn test() {
					take(x)
					take(x)
				}
			`,
			want: []string{
				"7:11-7:12: cannot move module-level value 'x' into an owned parameter. Make the parameter a borrow and pass `&x`",
				"8:11-8:12: cannot move module-level value 'x' into an owned parameter. Make the parameter a borrow and pass `&x`",
			},
		},
		"PassedToAnOwnedParameterBeforeACallReadsIt": {
			src: decls + `
				val x = {v: 1}
				fn peek() { readRead(&x, &x) }
				fn test() {
					take(x)
					peek()
				}
			`,
			want: []string{"8:11-8:12: cannot move module-level value 'x' into an owned parameter. Make the parameter a borrow and pass `&x`"},
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
			want: []string{"9:6-9:10: this call runs 'bump', which writes 'x' while it is borrowed as immutable"},
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
			want: []string{"13:6-13:15: this call runs 'bumpTwice', which writes 'x' while it is borrowed as immutable"},
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
		"ClosureCallsAModuleFunctionThatWrites": {
			src: decls + `
				val mut x = {v: 1}
				fn bump() { x.v = 5 }
				fn test() {
					val a = &x
					val g = fn () { bump() }
					g()
					readRead(a, a)
				}
			`,
			want: []string{"10:6-10:7: this call runs 'g', which writes 'x' while it is borrowed as immutable"},
		},
		"ClosureCallsAModuleFunctionBesideALocalOfTheSameName": {
			src: decls + `
				val mut x = {v: 1}
				fn bump() { x.v = 5 }
				fn test() {
					val mut x = {v: 2}
					val a = &x
					val g = fn () { bump() }
					g()
					readRead(a, a)
				}
			`,
			want: nil,
		},
		"ClassValueReadTwice": {
			src: decls + `
				class Foo { v: number }
				fn test() {
					val a = Foo
					val b = Foo
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
