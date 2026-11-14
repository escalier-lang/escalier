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

// TestBuiltinsNeedImport covers a builtin outside the prelude, referenced without an
// import of its package. Each one is unbound in the entry module.
func TestBuiltinsNeedImport(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantErrs []string
	}{
		{
			name:     "Console",
			src:      `val logged = console.log("hello")`,
			wantErrs: []string{"1:14-1:21: Unknown identifier: console"},
		},
		{
			name:     "DottedNamespace",
			src:      `val pi = Math.PI`,
			wantErrs: []string{"1:10-1:14: Unknown identifier: Math"},
		},
		{
			name:     "BareFunction",
			src:      `val n = parseInt("1")`,
			wantErrs: []string{"1:9-1:17: Unknown identifier: parseInt"},
		},
		{
			name:     "BareConstant",
			src:      `val n = NaN`,
			wantErrs: []string{"1:9-1:12: Unknown identifier: NaN"},
		},
		{
			name:     "Class",
			src:      `val e = Error("boom")`,
			wantErrs: []string{"1:9-1:14: Unknown identifier: Error"},
		},
		{
			name:     "TypeOnlyExport",
			src:      `declare val c: Console`,
			wantErrs: []string{"1:16-1:23: cannot find type `Console`"},
		},
		{
			name:     "WebFetch",
			src:      `val r = fetch("https://example.com")`,
			wantErrs: []string{"1:9-1:14: Unknown identifier: fetch"},
		},
		{
			name:     "WebType",
			src:      `declare val e: Event`,
			wantErrs: []string{"1:16-1:21: cannot find type `Event`"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, test.src)
			require.Equal(t, test.wantErrs, errs)
		})
	}
}

// TestBuiltinsResolveThroughImport covers a builtin outside the prelude reached through an
// import of its package, and the names that resolve with no import at all. A prelude
// export binds unprefixed, and a primitive's members and a regex literal's type are read
// from the library packages every run loads.
func TestBuiltinsResolveThroughImport(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "Console",
			src: `
				import "std:console"
				val logged = console.log("hello")
				declare val c: console.Console
			`,
			want: map[string]string{"logged": "undefined", "c": "Console"},
		},
		{
			name: "Math",
			src: `
				import "std:math"
				val pi = math.PI
			`,
			want: map[string]string{"pi": "number"},
		},
		{
			name: "Number",
			src: `
				import "std:number"
				val n = number.parseInt("1")
			`,
			want: map[string]string{"n": "number"},
		},
		{
			name: "Error",
			src: `
				import "std:error"
				val e = error.Error("boom")
				val typed: error.Error = e
			`,
			want: map[string]string{"e": "Error", "typed": "Error"},
		},
		{
			name: "WebFetch",
			src: `
				import "web:fetch"
				val r = fetch("https://example.com")
			`,
			want: map[string]string{"r": "Promise<Response>"},
		},
		{
			name: "Web",
			src: `
				import "web:web"
				declare val e: web.Event
			`,
			want: map[string]string{"e": "Event"},
		},
		{
			name: "APreludeNameNeedsNoImport",
			src:  `val xs: Array<number> = []`,
			want: map[string]string{"xs": "Array<number>"},
		},
		{
			name: "APrimitiveMemberNeedsNoImport",
			src:  `val n = "abc".length`,
			want: map[string]string{"n": "number"},
		},
		{
			name: "ARegexLiteralNeedsNoImport",
			src:  `val re = /ab+c/`,
			want: map[string]string{"re": "RegExp"},
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

// TestBuiltinsInScript asserts that a script reaches a builtin outside the prelude only
// through an import, the way a module does.
func TestBuiltinsInScript(t *testing.T) {
	_, _, errs := InferScriptAgainstStdlib(parseScript(t, `val pi = Math.PI`), benchTree, Options{})
	require.Equal(t, []string{"Unknown identifier: Math"}, errorMessagesOf(errs))

	scope, _, errs := InferScriptAgainstStdlib(parseScript(t, "import \"std:math\"\nval pi = math.PI"), benchTree, Options{})
	require.Empty(t, errs)
	b, ok := scope.OwnValue("pi")
	require.True(t, ok)
	require.Equal(t, "number", soltype.Print(schemeType(b.Schemes[0])))
}

// TestLibraryGlobalWriteNamesNoRedeclaration asserts that a write through a standard library
// global the library declares without `mut` reports the immutable binding without suggesting a
// declaration the code being checked cannot change. TypeScript accepts the same write, and
// whether Escalier should accept it too is an open question that #2321 tracks.
func TestLibraryGlobalWriteNamesNoRedeclaration(t *testing.T) {
	_, errs := inferAgainstTreeWithSpans(t, "import \"std:console\"\nfn f() {\n\tconsole.log = fn() {}\n}\n")
	require.Equal(t, []string{
		"3:2-3:23: cannot mutate 'console' because its binding is immutable; the standard library declares it without `mut`",
		"3:16-3:23: object is missing property: log",
	}, errs)
}
