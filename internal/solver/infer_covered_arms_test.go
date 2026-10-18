package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// coveredArmMsg is the CoveredMatchArmError message, shared by every case below so the span
// is the only thing each one spells out.
const coveredArmMsg = "this match arm is unreachable because the arms above it already cover every value; drop it"

// TestInferMatchReportsCoveredArms covers a `match` arm whose values the arms above it
// already cover. Such an arm is reported, along with every arm below it. An arm is left alone
// when the arms above it may miss a value, when coverage cannot be shown, and in a `catch`.
func TestInferMatchReportsCoveredArms(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantErrs []string
	}{
		{
			name: "a catch-all below arms covering a union",
			src: `declare val foo: string | number
val r = match foo {
	a: string => 1,
	b: number => 2,
	_ => 3,
}`,
			wantErrs: []string{"5:2-5:8: " + coveredArmMsg},
		},
		{
			name: "every arm below the covering arms",
			src: `declare val x: "a" | "b"
val r = match x {
	"a" => 1,
	"b" => 2,
	"c" => 3,
	_ => 4,
}`,
			wantErrs: []string{
				"5:2-5:10: " + coveredArmMsg,
				"6:2-6:8: " + coveredArmMsg,
			},
		},
		{
			name: "a catch-all below an arm covering an exact object",
			src: `declare val p: {x: number}
val r = match p {
	{x} => x,
	_ => 0,
}`,
			wantErrs: []string{"4:2-4:8: " + coveredArmMsg},
		},
		{
			name: "a catch-all below arms covering every length of a variadic tuple",
			src: `declare val t: [number, ...Array<string>]
val r = match t {
	[a] => 1,
	[a, b, ...rest] => 2,
	_ => 3,
}`,
			wantErrs: []string{"5:2-5:8: " + coveredArmMsg},
		},
		{
			name: "a catch-all below instance patterns covering a union of classes",
			src: `class Circle { r: number }
class Square { w: number }
declare val s: Circle | Square
val r = match s {
	Circle{r} => 1,
	Square{w} => 2,
	_ => 3,
}`,
			wantErrs: []string{"7:2-7:8: " + coveredArmMsg},
		},
		{
			// A value lacking the optional `x` fails `{x}`, so the catch-all still runs.
			name: "a catch-all below an arm testing an optional key",
			src: `declare val p: {x?: number}
val r = match p {
	{x} => x,
	_ => 0,
}`,
		},
		{
			name: "a catch-all over values the arms above miss",
			src: `declare val u: string | number | boolean
val r = match u {
	a: string => 1,
	b: number => 2,
	_ => 3,
}`,
		},
		{
			name: "a catch-all below a guarded arm",
			src: `declare val foo: string | number
declare val b: boolean
val r = match foo {
	a: string => 1,
	n: number if b => 2,
	_ => 3,
}`,
		},
		{
			// The arms above may cover `boolean`, but coverage over a non-union finite
			// carrier is not decided, so the catch-all is left alone.
			name: "a catch-all over a boolean",
			src: `declare val b: boolean
val r = match b {
	true => 1,
	false => 2,
	_ => 3,
}`,
		},
		{
			// The nested `{x}` can fail to match, so the second arm is reachable.
			name: "an arm below one with a nested sub-pattern",
			src: `declare val p: {a: {x: number}} | {a: {y: string}}
val r = match p {
	{a: {x}} => x,
	{a: {y}} => y,
}`,
		},
		{
			// The parameter's type is still being inferred, so a caller may pass a value
			// no arm above names.
			name: "a catch-all over an unannotated parameter",
			src: `fn f(x) {
	return match x {
		a: string => 1,
		b: number => 2,
		_ => 3,
	}
}
val r = f(true)`,
		},
		{
			// The `{x}` arm names an optional key, so the three arms together are not shown
			// to cover `p`. The first two are, so the arms below them are reported.
			name: "arms below a covering prefix followed by an optional-key arm",
			src: `declare val p: string | {x?: number, y: number}
val r = match p {
	s: string => 1,
	{y} => 2,
	{x} => 3,
	_ => 4,
}`,
			wantErrs: []string{
				"5:2-5:10: " + coveredArmMsg,
				"6:2-6:8: " + coveredArmMsg,
			},
		},
		{
			// A value no signature named can still reach a catch arm, so a catch-all below
			// arms covering every known throw is allowed.
			name: "a catch-all in a catch clause",
			src: `declare val c: boolean
val r = try {
	if c { throw "a" } else { throw 5 }
} catch {
	"a" => 0,
	5 => 1,
	_ => 2,
}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			if test.wantErrs == nil {
				require.Empty(t, messagesWithSpan(t, errs))
				return
			}
			require.Equal(t, test.wantErrs, messagesWithSpan(t, errs))
		})
	}
}
