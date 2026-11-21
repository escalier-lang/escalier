package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSignatureBoundFunctionsCheckInEitherOrder asserts that two module-level functions
// that call each other check with no errors in either source order when both write full
// signatures, and that each binds the type its signature writes.
func TestSignatureBoundFunctionsCheckInEitherOrder(t *testing.T) {
	const even = "fn even(n: number) -> boolean { return if n == 0 { true } else { odd(n - 1) } }\n"
	const odd = "fn odd(n: number) -> boolean { return if n == 0 { false } else { even(n - 1) } }\n"
	tests := []struct {
		name string
		src  string
	}{
		{name: "EvenFirst", src: even + odd},
		{name: "OddFirst", src: odd + even},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, "fn (n: number) -> boolean", values["even"])
			require.Equal(t, "fn (n: number) -> boolean", values["odd"])
		})
	}
}

// TestReaderBeforeClassSeesWrittenMemberType covers a function that reads a method or
// getter of a class declared after it. The member's written signature is its stub, so the
// reader checks against the written return type, a borrow included, before the class's
// bodies are walked.
func TestReaderBeforeClassSeesWrittenMemberType(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "Method",
			src:  "fn f(b: B) -> string { return b.m() }\nclass B { m(&self) -> number { return 1 }, }",
			want: []string{"1:31-1:36: cannot constrain number <: string"},
		},
		{
			name: "MethodReturningABorrow",
			src: "fn f(b: &B) -> &{v: string} { return b.m() }\n" +
				"class B { s: {v: number}, m(&self) -> &{v: number} { return &self.s }, }",
			want: []string{"1:38-1:43: cannot constrain number <: string"},
		},
		{
			name: "Getter",
			src:  "fn f(b: B) -> string { return b.v }\nclass B { get v(&self) -> number { return 1 }, }",
			want: []string{"1:31-1:34: cannot constrain number <: string"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
		})
	}
}

// TestInferredErrorTypesSolveTogether asserts that two functions that write `throws _`
// and call each other solve their error types together, so each raises what either body
// throws.
func TestInferredErrorTypesSolveTogether(t *testing.T) {
	values, errs := inferAgainstTreeWithSpans(t, `
		fn a(n: number) -> number throws _ { if n > 0 { return b(n - 1) } throw "a" }
		fn b(n: number) -> number throws _ { if n > 0 { return a(n - 1) } throw "b" }
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (n: number) -> number throws "b" | "a"`, values["a"])
	require.Equal(t, `fn (n: number) -> number throws "b" | "a"`, values["b"])
}
