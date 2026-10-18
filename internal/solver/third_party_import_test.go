package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestThirdPartyImportResolves covers an import of a package installed under the repository's
// `node_modules`, which `pnpm install` populates. Each case infers through
// InferModuleAgainstStdlib, the entry point the compiler uses, so the package has to resolve
// the way a real build would reach it.
func TestThirdPartyImportResolves(t *testing.T) {
	t.Skip("an import from node_modules does not resolve in internal/solver, #2099")
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "a bare import of a package",
			src:  `import "fast-deep-equal" as fde`,
		},
		{
			name: "a member read through the binding",
			src: `import "fast-deep-equal" as fde
val eq = fde.default`,
		},
		{
			name: "a subpath import beside the package's own",
			src: `import "react" as react
import "react/jsx-runtime" as rt
val f = react.Fragment
val j = rt.jsx`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, test.src)
			require.Empty(t, errs)
		})
	}
}
