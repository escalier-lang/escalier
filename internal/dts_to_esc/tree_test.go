package dts_to_esc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// TestCommittedTreeParses is half of §7's gate: every emitted file
// parses. The other half, that regenerating leaves the tree
// byte-identical, is what the `check_generated_tree` CI job proves.
//
// The two catch different things. Regenerating proves the writer is
// deterministic, and it stays green on output no reader can consume,
// because it compares the generator against itself. This reads the
// committed tree with the parser a consumer uses, so a converter that
// starts emitting a shape the grammar has no room for fails here.
func TestCommittedTreeParses(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "interop", "data")
	for _, sub := range []string{"std", "web"} {
		entries, err := os.ReadDir(filepath.Join(root, sub))
		require.NoError(t, err)
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".esc") {
				continue
			}
			path := filepath.Join(root, sub, e.Name())
			t.Run(sub+"/"+e.Name(), func(t *testing.T) {
				t.Parallel()
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				_, errs := parser.ParseLibFiles(t.Context(),
					[]*ast.Source{{ID: 0, Path: path, Contents: string(data)}})
				require.Empty(t, errs)
			})
		}
	}
}
