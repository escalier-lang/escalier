package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestScriptImports covers a script's imports. Each binds the way a module file's
// does, before the script's first statement runs.
func TestScriptImports(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		src   string
		value string
		want  string
	}{
		{
			name:  "a package's own-name value",
			src:   "import \"std:console\"\nval logged = console.log(\"hi\")",
			value: "logged",
			want:  "undefined",
		},
		{
			name:  "a namespace member",
			src:   "import \"std:math\"\nval pi = math.PI",
			value: "pi",
			want:  "number",
		},
		{
			name:  "an alias",
			src:   "import \"std:math\" as m\nval pi = m.PI",
			value: "pi",
			want:  "number",
		},
		{
			name:  "a later statement redeclares an imported name",
			src:   "import \"std:console\"\nval console = 5\nval n = console",
			value: "n",
			want:  "5",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			scope, _, errs := InferScriptAgainstStdlib(parseScript(t, tt.src), benchTree, Options{})
			require.Empty(t, errorMessagesOf(errs))
			b, ok := scope.OwnValue(tt.value)
			require.True(t, ok)
			require.Equal(t, tt.want, soltype.Print(schemeType(b.Schemes[0])))
		})
	}
}

// TestScriptImportReportsAnUnknownPackage asserts that a script's import of a package
// the tree does not hold is reported at the import.
func TestScriptImportReportsAnUnknownPackage(t *testing.T) {
	t.Parallel()

	_, _, errs := InferScriptAgainstStdlib(parseScript(t, "import \"std:nope\"\nval x = 1"), benchTree, Options{})
	require.Equal(t, []string{
		`cannot resolve import "std:nope": unknown package "nope" in std: scheme (no std/nope.esc under ` + benchTree + `)`,
	}, errorMessagesOf(errs))
}
