package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// inferAgainstTree infers src against the committed tree and renders the module's own
// value bindings, the way InferModuleAgainstStdlib leaves them.
func inferAgainstTree(t *testing.T, src string) (map[string]string, []string) {
	t.Helper()
	res := InferModuleAgainstStdlib(parseModule(t, src), benchTree)
	values := map[string]string{}
	for name, b := range res.Scope.values {
		values[name] = res.checker.renderValueBinding(b.Schemes[0])
	}
	msgs := make([]string, len(res.Errors))
	for i, e := range res.Errors {
		msgs[i] = e.Message()
	}
	return values, msgs
}

// TestAmbientBuiltinsResolveWithoutImport covers the three ambient binding rules. A
// bare `@js` path binds at the top level, a dotted one binds into a namespace named by
// its prefix, and a type binds under its declared name or beside its value.
func TestAmbientBuiltinsResolveWithoutImport(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "DottedPathBindsIntoNamespace",
			src:  `val pi = Math.PI`,
			want: map[string]string{"pi": "number"},
		},
		{
			name: "DottedPathCall",
			src:  `val parsed = JSON.parse("1")`,
			want: map[string]string{"parsed": "unknown"},
		},
		{
			name: "BarePathValue",
			src:  `val logged = console.log("hello")`,
			want: map[string]string{"logged": "unknown"},
		},
		{
			name: "BarePathFunction",
			src:  `val n = parseInt("1")`,
			want: map[string]string{"n": "number"},
		},
		{
			name: "BarePathConstant",
			src:  `val n = NaN`,
			want: map[string]string{"n": "number"},
		},
		{
			name: "ClassBindsValueAndType",
			src: `
				val e = Error("boom")
				val typed: Error = e
			`,
			want: map[string]string{"e": "Error", "typed": "Error"},
		},
		{
			name: "TypeOnlyExport",
			src:  `declare val c: Console`,
			want: map[string]string{"c": "Console"},
		},
		{
			name: "ClassTypeBindsBesideDottedValue",
			src:  `declare val collator: Intl.Collator`,
			want: map[string]string{"collator": "Collator"},
		},
		{
			name: "ExplicitImportStillBindsNamespace",
			src: `
				import "std:math"
				val pi = math.PI
			`,
			want: map[string]string{"pi": "number"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, errs := inferAgainstTree(t, test.src)
			require.Empty(t, errs)
			for name, want := range test.want {
				require.Equal(t, want, values[name], "value binding %q", name)
			}
		})
	}
}

// TestAmbientBuiltinsShadowedByModule asserts that a module's own declaration wins over
// an ambient name of the same sort.
func TestAmbientBuiltinsShadowedByModule(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "ValueShadowsNamespace",
			src: `
				val Math = {PI: "pie"}
				val pi = Math.PI
			`,
			want: map[string]string{"pi": `"pie"`},
		},
		{
			name: "FunctionShadowsValue",
			src: `
				fn parseInt(s: string) -> string { return s }
				val n = parseInt("1")
			`,
			want: map[string]string{"n": "string"},
		},
		{
			name: "ValueShadowsConstant",
			src: `
				val NaN = "not a number"
				val n = NaN
			`,
			want: map[string]string{"n": `"not a number"`},
		},
		{
			name: "ClassShadowsClass",
			src: `
				class Error { code: number }
				val e = Error(1)
				val code = e.code
			`,
			want: map[string]string{"code": "number"},
		},
		{
			// The alias renders under its own name, so the assignment is what shows it is the
			// module's `number` rather than the ambient interface.
			name: "TypeShadowsType",
			src: `
				type Console = number
				declare val c: Console
				val n: number = c
			`,
			want: map[string]string{"n": "number"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, errs := inferAgainstTree(t, test.src)
			require.Empty(t, errs)
			for name, want := range test.want {
				require.Equal(t, want, values[name], "value binding %q", name)
			}
		})
	}
}

// TestAmbientBuiltinsInScript asserts that a script reaches the ambient names the way a
// module does.
func TestAmbientBuiltinsInScript(t *testing.T) {
	scope, _, errs := InferScriptAgainstStdlib(parseScript(t, `val pi = Math.PI`), benchTree)
	require.Empty(t, errs)
	b, ok := scope.OwnValue("pi")
	require.True(t, ok)
	require.Equal(t, "number", soltype.Print(schemeType(b.Schemes[0])))
}
