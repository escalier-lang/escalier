package parser

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// stdlibSources reads every pseudo-package file of the committed stdlib tree, one
// source per file, numbered in path order.
func stdlibSources(tb testing.TB) []*ast.Source {
	tb.Helper()
	var sources []*ast.Source
	for _, scheme := range []string{"std", "web"} {
		paths, err := filepath.Glob(filepath.Join("..", "interop", "data", scheme, "*.esc"))
		require.NoError(tb, err)
		for _, path := range paths {
			contents, err := os.ReadFile(path)
			require.NoError(tb, err)
			sources = append(sources, &ast.Source{ID: len(sources), Path: filepath.Base(path), Contents: string(contents)})
		}
	}
	require.NotEmpty(tb, sources)
	return sources
}

// BenchmarkAttachModuleComments measures the comment pass over the committed stdlib
// parsed as one module, the shape a load of every package hands it.
func BenchmarkAttachModuleComments(b *testing.B) {
	module, errs := ParseLibFiles(context.Background(), stdlibSources(b))
	require.Empty(b, errs)
	for b.Loop() {
		ast.AttachModuleComments(module)
	}
}
