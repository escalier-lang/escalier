package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferMutParamMatchesPlainParam asserts that a `mut` marker on a parameter
// binding leaves the checker's result unchanged. src writes the marker and plain
// is the same program without it. Both must infer the same bindings and report
// the same errors, and want is the type both give `f`.
func TestInferMutParamMatchesPlainParam(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		plain string
		want  string
	}{
		{
			name:  "Annotated",
			src:   `fn f(mut p: {x: number}) -> number { return p.x }`,
			plain: `fn f(p: {x: number}) -> number { return p.x }`,
			want:  "fn (p: {x: number}) -> number",
		},
		{
			name:  "Unannotated",
			src:   `fn f(mut p) { return p.x }`,
			plain: `fn f(p) { return p.x }`,
			want:  "fn <T0>(p: {x: T0}) -> T0",
		},
		{
			name:  "Optional",
			src:   `fn f(mut p?: number) -> number { return 1 }`,
			plain: `fn f(p?: number) -> number { return 1 }`,
			want:  "fn (p?: number) -> number",
		},
		{
			name:  "Default",
			src:   `fn f(mut p: number = 1) -> number { return p }`,
			plain: `fn f(p: number = 1) -> number { return p }`,
			want:  "fn (p?: number) -> number",
		},
		{
			name:  "Rest",
			src:   `fn f(...mut rest: Array<number>) -> number { return rest.length }`,
			plain: `fn f(...rest: Array<number>) -> number { return rest.length }`,
			want:  "fn (...rest: Array<number>) -> number",
		},
		{
			name: "Called",
			src: `
				fn f(mut p: {x: number}) -> number { return p.x }
				val y = f({x: 1})
			`,
			plain: `
				fn f(p: {x: number}) -> number { return p.x }
				val y = f({x: 1})
			`,
			want: "fn (p: {x: number}) -> number",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, types, errs := inferSource(t, tt.src)
			plainValues, plainTypes, plainErrs := inferSource(t, tt.plain)
			require.Empty(t, plainErrs)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
			require.Equal(t, plainValues, values)
			require.Equal(t, plainTypes, types)
		})
	}
}
