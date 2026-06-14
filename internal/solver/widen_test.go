package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// M4 B3: an un-annotated `var` binding widens its initializer's literal types to
// their primitives, recursively through objects and tuples, so a mutable cell can
// later hold a different value of the same primitive. A `val` is a fixed
// singleton and keeps its literal type. These exercise the rendered binding type
// end-to-end through the real parser pipeline.
func TestInferVarLiteralWidening(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "scalar var widens to its primitive",
			src:  `var a = 5`,
			want: map[string]string{"a": "number"},
		},
		{
			name: "string and bool vars widen",
			src: `
				var s = "hi"
				var b = true
			`,
			want: map[string]string{"s": "string", "b": "boolean"},
		},
		{
			name: "val keeps its literal singleton",
			src:  `val a = 5`,
			want: map[string]string{"a": "5"},
		},
		{
			name: "object var widens each property",
			src:  `var p = {x: 0, y: 0}`,
			want: map[string]string{"p": "{x: number, y: number}"},
		},
		{
			name: "tuple var widens each element",
			src:  `var t = [1, 2]`,
			want: map[string]string{"t": "[number, number]"},
		},
		{
			name: "nesting widens through",
			src:  `var n = {p: {x: 0}}`,
			want: map[string]string{"n": "{p: {x: number}}"},
		},
		{
			name: "object val keeps its literals",
			src:  `val p = {x: 0, y: 0}`,
			want: map[string]string{"p": "{x: 0, y: 0}"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values)
		})
	}
}

// A widened `var` accepts a reassignment of a different value of the same
// primitive, while a value of a different primitive is still rejected — the
// binding is the widened primitive, not `any`.
func TestInferVarWideningReassignment(t *testing.T) {
	t.Run("same-primitive reassignment checks", func(t *testing.T) {
		values, _, errs := inferSource(t, "var a = 5\nfn f() { a = 6 }")
		require.Empty(t, errs)
		require.Equal(t, "number", values["a"])
	})
	t.Run("different-primitive reassignment rejected", func(t *testing.T) {
		src := "var a = 5\nfn f() { a = \"x\" }"
		_, _, errs := inferSource(t, src)
		requireBlame(t, src, errs, `cannot constrain "x" <: number`, `"x"`)
	})
}
