package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A subdirectory of `lib/` gives its declarations a qualified prefix, and the
// prefix binds as a namespace so a sibling file can name it.
//
// The flat qualified key carries the right type either way. What the binding
// adds is that `geo.x` resolves rather than reading `geo` as an unknown
// identifier.
func TestAPathDerivedNamespaceBinds(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		files map[string]string
		want  string
	}{
		"OneLevel": {
			files: map[string]string{
				"geo/shapes.esc": `val x: number = 1`,
				"index.esc":      `val read = geo.x`,
			},
			want: "number",
		},
		// Each ancestor prefix binds too, so the members of `geo.sub` are reached
		// by writing the whole path.
		"NestedDirectories": {
			files: map[string]string{
				"geo/sub/shapes.esc": `val x: string = "a"`,
				"index.esc":          `val read = geo.sub.x`,
			},
			want: "string",
		},
		// Two subdirectories declaring one name stay apart, since each member is
		// keyed under its own prefix.
		"TwoDirectoriesOneName": {
			files: map[string]string{
				"alpha/a.esc": `val x: number = 1`,
				"beta/b.esc":  `val x: string = "a"`,
				"index.esc":   `val read = beta.x`,
			},
			want: "string",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			values, _, errs := inferModule(parseModuleFiles(t, test.files))
			require.Empty(t, errorMessagesOf(errs))
			require.Equal(t, test.want, values["read"])
		})
	}
}

// A member bound after the file that reads it is still found, which is what the
// walk pushing each binding into its namespace buys. `read` is inferred before
// `y` in no particular order, so whichever goes second has to reach the other.
func TestAPathDerivedNamespaceSeesLateMembers(t *testing.T) {
	t.Parallel()

	values, _, errs := inferModule(parseModuleFiles(t, map[string]string{
		"geo/a.esc": `val first: number = 1`,
		"geo/b.esc": `val second = geo.first`,
		"index.esc": `val read = geo.second`,
	}))

	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "number", values["read"])
}
