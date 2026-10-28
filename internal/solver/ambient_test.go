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
	res := InferModuleAgainstStdlib(parseModule(t, src), benchTree, Options{})
	msgs := make([]string, len(res.Errors))
	for i, e := range res.Errors {
		msgs[i] = e.Message()
	}
	return renderedValues(res), msgs
}

// inferAgainstTreeWithSpans is inferAgainstTree with each error rendered as
// "line:col-line:col: message", the form msgWithSpan produces.
func inferAgainstTreeWithSpans(t *testing.T, src string) (map[string]string, []string) {
	t.Helper()
	res := InferModuleAgainstStdlib(parseModule(t, src), benchTree, Options{})
	return renderedValues(res), messagesWithSpan(t, res.Errors)
}

// renderedValues renders each top-level value binding of res by name.
func renderedValues(res *ModuleResult) map[string]string {
	values := map[string]string{}
	for name, b := range res.Scope.values {
		values[name] = res.checker.renderValueBinding(b.Schemes[0])
	}
	return values
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
			src: `
				val logged = console.log("hello")
				val nothing: undefined = console.log("again")
			`,
			want: map[string]string{"logged": "undefined"},
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
			name: "WebFetchBindsAtTheTopLevel",
			src:  `val r = fetch("https://example.com")`,
			want: map[string]string{"r": "Promise<Response>"},
		},
		{
			name: "WebCoreTypeBinds",
			src:  `declare val e: Event`,
			want: map[string]string{"e": "Event"},
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
	scope, _, errs := InferScriptAgainstStdlib(parseScript(t, `val pi = Math.PI`), benchTree, Options{})
	require.Empty(t, errs)
	b, ok := scope.OwnValue("pi")
	require.True(t, ok)
	require.Equal(t, "number", soltype.Print(schemeType(b.Schemes[0])))
}

// TestAmbientNamesOptOut covers a run with Options.NoAmbientNames set. No ambient name
// binds, so a builtin needs its import. The ambient packages still load, so a primitive's
// members and a regex literal's type resolve without one.
func TestAmbientNamesOptOut(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		want     map[string]string
		wantErrs []string
	}{
		{
			name:     "AnAmbientNameIsUnbound",
			src:      `val pi = Math.PI`,
			wantErrs: []string{"Unknown identifier: Math"},
		},
		{
			name:     "AWebNameIsUnbound",
			src:      `val r = fetch("https://example.com")`,
			wantErrs: []string{"Unknown identifier: fetch"},
		},
		{
			name: "AnImportStillBinds",
			src: `
				import "std:math"
				val pi = math.PI
			`,
			want: map[string]string{"pi": "number"},
		},
		{
			name: "APreludeNameStillBinds",
			src:  `val xs: Array<number> = []`,
			want: map[string]string{"xs": "Array<number>"},
		},
		{
			name: "APrimitiveMemberStillReads",
			src:  `val n = "abc".length`,
			want: map[string]string{"n": "number"},
		},
		{
			name: "ARegexLiteralStillTypes",
			src:  `val re = /ab+c/`,
			want: map[string]string{"re": "RegExp"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			res := InferModuleAgainstStdlib(parseModule(t, test.src), benchTree, Options{NoAmbientNames: true})
			if test.wantErrs != nil {
				require.Equal(t, test.wantErrs, errorMessagesOf(res.Errors))
				return
			}
			require.Empty(t, errorMessagesOf(res.Errors))
			values := renderedValues(res)
			for name, want := range test.want {
				require.Equal(t, want, values[name], "value binding %q", name)
			}
		})
	}
}

// TestAmbientGlobalWriteNamesNoRedeclaration asserts that a write through a standard library
// global the library declares without `mut` reports the immutable binding without suggesting a
// declaration the code being checked cannot change. TypeScript accepts the same write, and
// whether Escalier should accept it too is an open question that #2321 tracks.
func TestAmbientGlobalWriteNamesNoRedeclaration(t *testing.T) {
	_, errs := inferAgainstTreeWithSpans(t, "fn f() {\n\tconsole.log = fn() {}\n}\n")
	require.Equal(t, []string{
		"2:2-2:23: cannot mutate 'console' because its binding is immutable; the standard library declares it without `mut`",
		"2:16-2:23: object is missing property: log",
	}, errs)
}
