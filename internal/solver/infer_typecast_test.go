package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferTypeCast covers `expr : T`, ported from the old checker's
// internal/checker/tests/typecast_test.go. A cast the operand satisfies yields the
// annotation. `any` resolves to `unknown` on the solver, so a cast to `any` yields
// `unknown` where the old checker yields `any`.
func TestInferTypeCast(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "NumberToNumber", src: "val x = 5\nval y = x : number", want: "number"},
		{name: "StringToString", src: "val x = \"hello\"\nval y = x : string", want: "string"},
		{name: "BooleanToBoolean", src: "val x = true\nval y = x : boolean", want: "boolean"},
		{name: "NumberToAny", src: "val x = 42\nval y = x : any", want: "unknown"},
		{name: "Chained", src: "val x = 5\nval y = x : number : any", want: "unknown"},
		{name: "NullToAny", src: "val x = null\nval y = x : any", want: "unknown"},
		{name: "UndefinedToAny", src: "val x = undefined\nval y = x : any", want: "unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, _, errs := inferSource(t, test.src)
			require.Empty(t, errs)
			require.Equal(t, test.want, values["y"])
		})
	}
}

// TestInferTypeCastRejectsOperand covers a cast the operand does not satisfy. The
// failure blames the operand with the annotation as its related span, and the cast
// still yields the annotation.
func TestInferTypeCastRejectsOperand(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
		node string
		ann  string
	}{
		{
			name: "StringToNumber",
			src:  "val y = \"hello\" : number",
			want: `1:9-1:16: cannot constrain "hello" <: number`,
			node: `"hello"`,
			ann:  "number",
		},
		{
			name: "NumberToString",
			src:  "val y = 5 : string",
			want: "1:9-1:10: cannot constrain 5 <: string",
			node: "5",
			ann:  "string",
		},
		{
			name: "BooleanToNumber",
			src:  "val y = true : number",
			want: "1:9-1:13: cannot constrain true <: number",
			node: "true",
			ann:  "number",
		},
		{
			name: "ObjectToNumber",
			src:  "val y = {a: 1} : number",
			want: "1:9-1:15: cannot constrain object <: number",
			node: "{a: 1}",
			ann:  "number",
		},
		{
			name: "ArrayToString",
			src:  "val y = [1, 2, 3] : string",
			want: "1:9-1:18: cannot constrain tuple <: string",
			node: "[1, 2, 3]",
			ann:  "string",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, _, errs := inferSource(t, test.src)
			requireBlame(t, test.src, errs, test.want, test.node, test.ann)
			require.NotEqual(t, "error", values["y"])
		})
	}
}

// TestInferTypeCastUnresolvedAnnotation asserts that a cast to a type that does not
// resolve reports the annotation and yields the recovery sentinel.
func TestInferTypeCastUnresolvedAnnotation(t *testing.T) {
	values, _, errs := inferSource(t, "val y = 5 : Nope")
	require.Len(t, errs, 1)
	require.Equal(t, "cannot find type `Nope`", errs[0].Message())
	require.Equal(t, "error", values["y"])
}
