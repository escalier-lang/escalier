package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferTemplateLit covers a template literal, which is a `string` whatever it
// interpolates, provided each interpolation prints its value.
func TestInferTemplateLit(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "NoInterpolation", src: "val x = `hi`"},
		{name: "Number", src: "val n = 1\nval x = `a${n}b`"},
		{name: "String", src: "val x = `a${\"s\"}b`"},
		{name: "Boolean", src: "val x = `${true}`"},
		{name: "Several", src: "val x = `${1} and ${\"two\"}`"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, _, errs := inferSource(t, test.src)
			require.Empty(t, errs)
			require.Equal(t, "string", values["x"])
		})
	}
}

// TestInferTemplateLitRejectsInterpolation asserts that an interpolation which does
// not print its value is rejected at its own node.
func TestInferTemplateLitRejectsInterpolation(t *testing.T) {
	t.Run("symbol", func(t *testing.T) {
		src := "declare val s: symbol\nval x = `a${s}b`"
		_, _, errs := inferSource(t, src)
		requireBlame(t, src, errs, "2:13-2:14: cannot constrain symbol <: string | number | boolean | bigint", "s")
	})
	t.Run("object", func(t *testing.T) {
		src := "val x = `${{a: 1}}`"
		_, _, errs := inferSource(t, src)
		requireBlame(t, src, errs, "1:12-1:18: cannot constrain object <: string | number | boolean | bigint", "{a: 1}")
	})
}

// TestInferTaggedTemplateLit covers a tagged template literal, which calls its tag
// with the quasis and then each interpolation.
func TestInferTaggedTemplateLit(t *testing.T) {
	t.Run("array of strings", func(t *testing.T) {
		values, errs := inferAgainstTree(t, "declare fn tag(strings: Array<string>, ...values: Array<number>) -> boolean\nval x = tag`a${1}b${2}`")
		require.Empty(t, errs)
		require.Equal(t, "boolean", values["x"])
	})
	t.Run("TemplateStringsArray", func(t *testing.T) {
		values, errs := inferAgainstTree(t, "declare fn tag(strings: TemplateStringsArray) -> number\nval x = tag`plain`")
		require.Empty(t, errs)
		require.Equal(t, "number", values["x"])
	})
	t.Run("rejected interpolation", func(t *testing.T) {
		_, errs := inferAgainstTree(t, "declare fn tag(strings: Array<string>, ...values: Array<number>) -> boolean\nval x = tag`a${\"s\"}`")
		require.Equal(t, []string{`cannot constrain "s" <: number`}, errs)
	})
}
