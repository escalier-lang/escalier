package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestModuleBindingMovesAndBorrows covers moves and borrows of a binding declared at a
// module's top level. A function's body tracks each module-level binding it reaches, and a
// call of a module function takes a loan of each module-level binding that function reaches.
func TestModuleBindingMovesAndBorrows(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// Moving x out of the module-level binding would leave test callable only once, and an
		// exported test gives no way to tell whether it already ran. So the first move is an
		// error. The second reports only that x was already moved.
		"MovedTwiceInOneFunction": {
			src: `
				declare fn take(x: {v: number}) -> undefined
				val x = {v: 1}
				fn test() {
					take(x)
					take(x)
				}
			`,
			want: []string{
				"5:11-5:12: cannot move module-level value 'x' inside a function. Borrow it with `&x`",
				"6:11-6:12: use of moved value 'x'",
			},
		},
		"CallReadsAfterTheMove": {
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined
				declare fn take(x: {v: number}) -> undefined
				val x = {v: 1}
				fn peek() { readRead(&x, &x) }
				fn test() {
					take(x)
					peek()
				}
			`,
			want: []string{
				"7:11-7:12: cannot move module-level value 'x' inside a function. Borrow it with `&x`",
				"8:6-8:10: this call runs 'peek', which reads 'x' after it was moved",
			},
		},
		"CallWritesWhileBorrowed": {
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined
				val mut x = {v: 1}
				fn bump() { x.v = 5 }
				fn test() {
					val a = &x
					bump()
					readRead(a, a)
				}
			`,
			want: []string{"7:6-7:10: this call runs 'bump', which writes 'x' while it is borrowed as immutable"},
		},
		// The awaited call runs bump before test resumes, so a reads x across the write.
		"AwaitedCallWritesWhileBorrowed": {
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined
				val mut x = {v: 1}
				async fn bump() -> Promise<undefined> { x.v = 5 }
				async fn test() -> Promise<undefined> {
					val a = &x
					await bump()
					readRead(a, a)
				}
			`,
			want: []string{"7:12-7:16: this call runs 'bump', which writes 'x' while it is borrowed as immutable"},
		},
		"CallReadsWhileBorrowed": {
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined
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
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined
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
			want: []string{"11:6-11:15: this call runs 'bumpTwice', which writes 'x' while it is borrowed as immutable"},
		},
		"ParameterShadowsAModuleBindingACalleeWrites": {
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined
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
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined
				val mut x = {v: 1}
				fn bump() { x.v = 5 }
				fn test() {
					val a = &x
					val g = fn () { bump() }
					g()
					readRead(a, a)
				}
			`,
			want: []string{"8:6-8:7: this call runs 'g', which writes 'x' while it is borrowed as immutable"},
		},
		"ClosureCallsAModuleFunctionBesideALocalOfTheSameName": {
			src: `
				declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined
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
			src: `
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
