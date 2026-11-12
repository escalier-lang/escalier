package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPseudoPackageBindsAsItsOwnNameValue covers an import of a pseudo-package that
// exports a value under the name the import binds. The binding reads as that value
// and as the package's namespace at once.
func TestPseudoPackageBindsAsItsOwnNameValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "console is the value and names the type",
			src: `import "std:console"
				fn log() { console.log("x") }
				val c: console.Console = console`,
			want: map[string]string{"log": "fn () -> undefined", "c": "Console"},
		},
		{
			name: "fetch is a call and names the response type",
			src: `import "web:fetch"
				val r = fetch("https://example.com")
				val h: fetch.Response | undefined = undefined`,
			want: map[string]string{"r": "Promise<Response>", "h": "Response | undefined"},
		},
		{
			// `web:performance` and `web:crypto` declare their values `val mut`, so a
			// method that takes `&mut self` checks.
			name: "performance and crypto call methods that take &mut self",
			src: `import "web:performance"
				import "web:crypto"
				fn stamp() -> number { return performance.now() }
				fn id() -> string { return crypto.randomUUID() }`,
			want: map[string]string{"stamp": "fn () -> number", "id": "fn () -> string"},
		},
		{
			name: "a package exporting no value of its name stays a namespace",
			src: `import "std:math"
				val p = math.PI`,
			want: map[string]string{"p": "number"},
		},
		{
			// The value is chosen by the name the URI derives and bound under the name
			// the import writes, the way an npm package's `export =` value is.
			name: "an alias binds the value under the alias",
			src: `import "std:console" as c
				fn log() { c.log("x") }
				val k: c.Console = c`,
			want: map[string]string{"log": "fn () -> undefined", "k": "Console"},
		},
		{
			name: "a value the module declares keeps the name",
			src: `import "std:console"
				val console = 5
				val n = console`,
			want: map[string]string{"n": "5"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := InferModuleAgainstStdlib(parseModule(t, tt.src), committedTree, Options{})
			require.Empty(t, errorMessagesOf(res.Errors))
			values := renderedValues(res)
			for name, want := range tt.want {
				require.Equal(t, want, values[name], "value binding %q", name)
			}
		})
	}
}
