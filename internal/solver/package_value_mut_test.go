package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPackageValueLendsMutAccessThroughItsBinding covers a member read off the value a
// pseudo-package exports under the name its import binds. The read goes through the
// package's namespace, and the value lends the mutable access a `&mut self` method needs
// exactly when the package declares it `val mut`.
func TestPackageValueLendsMutAccessThroughItsBinding(t *testing.T) {
	t.Parallel()

	const counterClass = `export declare class Counter {
		n: number,
		state: {count: number},
		bump(&mut self) -> undefined,
		read(&self) -> number,
	}
	`
	mutCounter := map[string]string{
		"web/counter.esc": counterClass + "export declare val mut counter: Counter\n",
	}
	immutableCounter := map[string]string{
		"web/counter.esc": counterClass + "export declare var counter: Counter\n",
	}

	tests := []struct {
		name   string
		files  map[string]string
		src    string
		errors []string
	}{
		{
			name:  "AMutValueCallsAMutSelfMethod",
			files: mutCounter,
			src: `import "web:counter"
				fn f() -> undefined { return counter.bump() }`,
		},
		{
			name:  "AMutValueReadThroughAnAlias",
			files: mutCounter,
			src: `import "web:counter" as c
				fn f() -> undefined { return c.bump() }`,
		},
		{
			name:  "AMutValueReadThroughAConstantKey",
			files: mutCounter,
			src: `import "web:counter"
				fn f() -> undefined { return counter["bump"]() }`,
		},
		{
			name:  "AnImmutableValueRejectsAMutSelfMethod",
			files: immutableCounter,
			src: `import "web:counter"
				fn f() -> undefined { return counter.bump() }`,
			errors: []string{"cannot mutate 'counter' because its binding is immutable; the standard library declares it without `mut`"},
		},
		{
			name:  "AMutValueTakesAFieldWrite",
			files: mutCounter,
			src: `import "web:counter"
				fn f() { counter.n = 1 }`,
		},
		{
			name:  "AnImmutableValueRejectsAFieldWrite",
			files: immutableCounter,
			src: `import "web:counter"
				fn f() { counter.n = 1 }`,
			errors: []string{"cannot mutate 'counter' because its binding is immutable; the standard library declares it without `mut`"},
		},
		{
			name:  "AMutValueLendsAMutableBorrowOfAField",
			files: mutCounter,
			src: `import "web:counter"
				fn f() {
					val s = &mut counter.state
					s.count = 1
				}`,
		},
		{
			name:  "AnImmutableValueRejectsAMutableBorrowOfAField",
			files: immutableCounter,
			src: `import "web:counter"
				fn f() {
					val s = &mut counter.state
					s.count = 1
				}`,
			errors: []string{"cannot mutate 'counter' because its binding is immutable; the standard library declares it without `mut`"},
		},
		{
			name:  "AnImmutableValueCallsASelfMethod",
			files: immutableCounter,
			src: `import "web:counter"
				fn f() -> number { return counter.read() }`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := inferAgainstCyclicStdlib(t, tt.src, tt.files)
			if tt.errors == nil {
				require.Empty(t, errorMessagesOf(res.Errors))
				return
			}
			require.Equal(t, tt.errors, errorMessagesOf(res.Errors))
		})
	}
}
