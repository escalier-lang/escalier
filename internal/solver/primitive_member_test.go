package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A member read off a primitive resolves through the wrapper class the standard library
// declares for it. A literal reads through its primitive's wrapper, and a receiver solved
// to a primitive after the read reads through the same class. The wrappers come from
// testdata/stdlib.
func TestPrimitiveMemberReadsThroughItsWrapper(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "MethodOnANumberLiteral",
			src:  `val r = (1.5).toFixed(2)`,
			want: "string",
		},
		{
			name: "MethodOnAString",
			src: `
				declare val s: string
				val r = s.toUpperCase()
			`,
			want: "string",
		},
		{
			name: "MethodReadAsAValue",
			src: `
				declare val s: string
				val r = s.charAt
			`,
			want: "fn (pos: number) -> string",
		},
		{
			name: "FieldOnAStringLiteral",
			src:  `val r = "abc".length`,
			want: "number",
		},
		{
			name: "MethodOnABooleanLiteral",
			src:  `val r = true.valueOf()`,
			want: "boolean",
		},
		{
			name: "MethodOnABigint",
			src: `
				declare val b: bigint
				val r = b.toString(16)
			`,
			want: "string",
		},
		{
			name: "FieldOnASymbol",
			src: `
				declare val s: symbol
				val r = s.description
			`,
			want: "string | undefined",
		},
		{
			name: "FieldOnAUnionOfLiterals",
			src: `
				declare val u: "a" | "bc"
				val r = u.length
			`,
			want: "number",
		},
		{
			name: "MethodOnAUnionOfPrimitives",
			src: `
				declare val u: number | string
				val r = u.valueOf
			`,
			want: "(fn () -> number) | (fn () -> string)",
		},
		{
			name: "DestructuringAString",
			src: `
				val {length} = "abc"
				val r = length
			`,
			want: "number",
		},
		{
			name: "CallbackParameterSolvedToANumber",
			src: `
				declare fn apply(f: fn (n: number) -> string) -> string
				val r = apply(fn (n) { return n.toFixed(2) })
			`,
			want: "string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errorMessagesOf(errs))
			require.Equal(t, tt.want, values["r"])
		})
	}
}

// A member the wrapper does not declare is missing from the primitive. Boxing applies only
// to a read, so a primitive still does not satisfy an object type written as an annotation.
func TestPrimitiveMemberRejections(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "MissingMethod",
			src:  `val r = (1.5).nope`,
			want: []string{"object is missing property: nope"},
		},
		{
			name: "MissingMemberOnACallbackParameter",
			src: `
				declare fn apply(f: fn (n: number) -> string) -> string
				val r = apply(fn (n) { return n.nope })
			`,
			want: []string{"object is missing property: nope"},
		},
		{
			// A receiver solved after the read reads only a method that borrows it
			// immutably, since the read has no receiver binding to check a `&mut self`
			// against.
			name: "MutatingMethodOnACallbackParameter",
			src: `
				class C {
					n: number,
					bump(&mut self) -> number { return 1 },
				}
				declare fn apply(f: fn (c: C) -> number) -> number
				val r = apply(fn (c) { return c.bump() })
			`,
			want: []string{"object is missing property: bump"},
		},
		{
			name: "OpenObjectAnnotation",
			src:  `val r: {...} = 5`,
			want: []string{"cannot constrain 5 <: object"},
		},
		{
			name: "AnnotationNamingAWrapperMember",
			src:  `val r: {valueOf: fn () -> number, ...} = 5`,
			want: []string{"cannot constrain 5 <: object"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, errorMessagesOf(errs))
		})
	}
}
