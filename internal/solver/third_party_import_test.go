package solver

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestThirdPartyImportResolves covers an import of a package installed under the repository's
// `node_modules`, which `pnpm install` populates. Each case infers through
// InferModuleAgainstStdlib, the entry point the compiler uses, with the repository root as
// the package directory, so the package resolves the way a real build reaches it. A case
// that names the issues blocking it is skipped until they land.
func TestThirdPartyImportResolves(t *testing.T) {
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	tests := []struct {
		name      string
		src       string
		blockedBy string
	}{
		{
			name:      "a package declared with export =",
			src:       "import \"fast-deep-equal\" as fde\nval same = fde(1, 1)",
			blockedBy: "#2108",
		},
		{
			name: "a package whose types name a readonly array",
			src:  `import "csstype" as css`,
		},
		{
			name: "a subpath import beside the package's own",
			src: `import "react" as react
import "react/jsx-runtime" as rt
val f = react.Fragment
val j = rt.jsx`,
			blockedBy: "#2108, #2110 and #328",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.blockedBy != "" {
				t.Skip("blocked by " + test.blockedBy)
			}
			res := InferModuleAgainstStdlib(parseModule(t, test.src), benchTree, Options{PackageDir: root})
			require.Empty(t, errorMessagesOf(res.Errors))
		})
	}
}
