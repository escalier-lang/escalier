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

// A `namespace` block written in a subdirectory hangs off the namespace the
// directory introduced, so the two prefixes are walked as one path.
//
// The block pass mints `geo.shapes` and the path pass mints `geo`, in that
// order, so linking them is neither pass's job. Reading `geo.shapes.x` is what
// walks `geo` to reach `shapes`.
func TestABlockInASubdirectoryHangsOffThePathNamespace(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		files map[string]string
		want  string
	}{
		"OneBlock": {
			files: map[string]string{
				"geo/file.esc": "namespace shapes {\n  val x: number = 1\n}",
				"index.esc":    `val read = geo.shapes.x`,
			},
			want: "number",
		},
		// A block inside a block is linked by the block pass, and the directory
		// prefix above both still has to reach the outer one.
		"NestedBlocks": {
			files: map[string]string{
				"geo/file.esc": "namespace shapes {\n  namespace flat {\n    val x: string = \"a\"\n  }\n}",
				"index.esc":    `val read = geo.shapes.flat.x`,
			},
			want: "string",
		},
		// Two directories may each hold a block of one name, since each block is
		// keyed under the directory's prefix.
		"TwoDirectoriesOneBlockName": {
			files: map[string]string{
				"alpha/a.esc": "namespace shapes {\n  val x: number = 1\n}",
				"beta/b.esc":  "namespace shapes {\n  val x: string = \"a\"\n}",
				"index.esc":   `val read = beta.shapes.x`,
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
