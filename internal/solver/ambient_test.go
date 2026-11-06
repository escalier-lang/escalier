package solver

import (
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/gkampitakis/go-snaps/snaps"
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

// globalsChecker returns a checker against the committed tree whose globals scope is
// bound, the scope an npm package's `.d.ts` reads.
func globalsChecker(t *testing.T) *checker {
	t.Helper()
	groups, err := BuildPackageClosure(benchTree, append(ambientPackages, webPreludeURI))
	require.NoError(t, err)
	c := loadAmbientChecker(benchTree, groups, Options{}).fork(StdlibSource(benchTree), StdlibGroupSource(benchTree), groups)
	c.bindGlobals()
	return c
}

// TestGlobalsScopeBindingRules covers the three rules bindGlobals binds a package's
// exports by. A bare `@js` path binds at the top level, a dotted one binds into a
// namespace named by its prefix, and a type binds under its declared name or beside
// its value. The globals scope reaches `web:prelude` through its parent.
func TestGlobalsScopeBindingRules(t *testing.T) {
	t.Parallel()

	c := globalsChecker(t)
	globals := c.globalsScope()

	values := []struct {
		name string
		path []string
		want string
	}{
		{name: "a dotted path binds into a namespace", path: []string{"Math", "PI"}, want: "number"},
		{name: "a bare path binds a constant", path: []string{"NaN"}, want: "number"},
		{name: "a bare path binds a value", path: []string{"console"}, want: "Console"},
		{name: "a class binds its value", path: []string{"Error"}},
		{name: "a web package binds at the top level", path: []string{"fetch"}},
		{name: "a web package class binds its value", path: []string{"Blob"}},
		{name: "the web prelude is reached through the parent", path: []string{"AbortController"}},
	}
	for _, tt := range values {
		t.Run(tt.name, func(t *testing.T) {
			var b ValueBinding
			var found bool
			if len(tt.path) == 1 {
				b, found = globals.GetValue(tt.path[0])
			} else {
				ns, held := globals.GetNamespace(tt.path[0])
				require.True(t, held)
				b, found = ns.Values[tt.path[1]]
			}
			require.True(t, found)
			if tt.want != "" {
				require.Equal(t, tt.want, c.renderValueBinding(b.Schemes[0]))
			}
		})
	}

	types := []struct {
		name string
		path []string
	}{
		{name: "a type-only export binds under its name", path: []string{"Console"}},
		{name: "a class binds its type beside its value", path: []string{"Error"}},
		{name: "a class type binds beside a dotted value", path: []string{"Intl", "Collator"}},
		{name: "a web package type binds", path: []string{"ReadableStream"}},
		{name: "a web prelude type is reached through the parent", path: []string{"Event"}},
	}
	for _, tt := range types {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.path) == 1 {
				_, found := globals.GetType(tt.path[0])
				require.True(t, found)
				return
			}
			ns, held := globals.GetNamespace(tt.path[0])
			require.True(t, held)
			require.Contains(t, ns.Types, tt.path[1])
		})
	}
}

// TestWebPreludeShadowedByModule asserts that a module's own declaration wins over a
// `web:prelude` name of the same sort.
func TestWebPreludeShadowedByModule(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "ClassShadowsClass",
			src: `
				class Event { code: number }
				val e = Event(1)
				val code = e.code
			`,
			want: map[string]string{"code": "number"},
		},
		{
			// The alias renders under its own name, so the assignment is what shows it is the
			// module's `number` rather than the prelude's interface.
			name: "TypeShadowsType",
			src: `
				type EventInit = number
				declare val i: EventInit
				val n: number = i
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

// TestWebPreludeInScript asserts that a script reaches the `web:prelude` names the way a
// module does, and no builtin outside the `*:prelude` packages.
func TestWebPreludeInScript(t *testing.T) {
	scope, _, errs := InferScriptAgainstStdlib(parseScript(t, `val c = AbortController()`), benchTree, Options{})
	require.Empty(t, errs)
	b, ok := scope.OwnValue("c")
	require.True(t, ok)
	require.Equal(t, "AbortController", soltype.Print(schemeType(b.Schemes[0])))

	_, _, errs = InferScriptAgainstStdlib(parseScript(t, `val pi = Math.PI`), benchTree, Options{})
	require.Equal(t, []string{"Unknown identifier: Math"}, errorMessagesOf(errs))
}

// TestProjectFileAmbientNames covers what a project's own file reaches with no import.
// The `*:prelude` packages are its whole ambient layer, so every other builtin needs its
// package's import. The packages that type a literal still load, so a primitive's members
// and a regex literal's type resolve without one.
func TestProjectFileAmbientNames(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		want     map[string]string
		wantErrs []string
	}{
		{
			name:     "AStdNameIsUnbound",
			src:      `val pi = Math.PI`,
			wantErrs: []string{"Unknown identifier: Math"},
		},
		{
			name:     "AStdClassIsUnbound",
			src:      `val e = Error("boom")`,
			wantErrs: []string{"Unknown identifier: Error"},
		},
		{
			name:     "AWebNameOutsideThePreludeIsUnbound",
			src:      `val r = fetch("https://example.com")`,
			wantErrs: []string{"Unknown identifier: fetch"},
		},
		{
			name: "AnImportBinds",
			src: `
				import "std:math"
				val pi = math.PI
			`,
			want: map[string]string{"pi": "number"},
		},
		{
			name: "APreludeNameBinds",
			src:  `val xs: Array<number> = []`,
			want: map[string]string{"xs": "Array<number>"},
		},
		{
			name: "AWebPreludeNameBinds",
			src: `
				declare val e: Event
				val t = e.type
			`,
			want: map[string]string{"t": "string"},
		},
		{
			name: "APrimitiveMemberReads",
			src:  `val n = "abc".length`,
			want: map[string]string{"n": "number"},
		},
		{
			name: "ARegexLiteralTypes",
			src:  `val re = /ab+c/`,
			want: map[string]string{"re": "RegExp"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			res := InferModuleAgainstStdlib(parseModule(t, test.src), benchTree, Options{})
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
	_, errs := inferAgainstTreeWithSpans(t, "import \"std:console\"\nfn f() {\n\tconsole.log = fn() {}\n}\n")
	require.Equal(t, []string{
		"3:2-3:23: cannot mutate 'console' because its binding is immutable; the standard library declares it without `mut`",
		"3:16-3:23: object is missing property: log",
	}, errs)
}

// TestAmbientNameSets asserts the names each ambient layer binds. A project's own file
// reaches the web prelude scope's names, and an npm package's `.d.ts` reaches the
// globals scope's names as well. A change to either set shows here as a diff.
func TestAmbientNameSets(t *testing.T) {
	t.Parallel()

	c := globalsChecker(t)
	render := func(s *Scope) string {
		return "values: " + strings.Join(s.ValueNames(), ", ") +
			"\ntypes: " + strings.Join(s.TypeNames(), ", ") +
			"\nnamespaces: " + strings.Join(s.NamespaceNames(), ", ")
	}

	t.Run("a project's own file", func(t *testing.T) {
		snaps.MatchInlineSnapshot(t, render(c.webPreludeScope()), snaps.Inline(`values: AbortController, AbortSignal, DOMException, Event, EventTarget, MessageEvent, TextDecoder, TextEncoder
types: AbortController, AbortSignal, AbortSignalEventMap, AddEventListenerOptions, AllowSharedBufferSource, BufferSource, DOMException, DOMHighResTimeStamp, Event, EventInit, EventListener, EventListenerObject, EventListenerOptions, EventListenerOrEventListenerObject, EventTarget, MessageEvent, MessageEventInit, TextDecodeOptions, TextDecoder, TextDecoderCommon, TextDecoderOptions, TextEncoder, TextEncoderCommon, TextEncoderEncodeIntoResult
namespaces: `))
	})
	t.Run("an npm package's .d.ts", func(t *testing.T) {
		snaps.MatchSnapshot(t, render(c.globalsScope()))
	})
}
