package compiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindSourceFiles(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{
		"lib/index.esc",
		"lib/util/strings.esc",
		"lib/README.md",
		"bin/main.esc",
		"build/lib/index.esc",
	} {
		path := filepath.Join(root, filepath.FromSlash(file))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, nil, 0o644))
	}
	t.Chdir(root)

	files, err := FindSourceFiles()
	require.NoError(t, err)
	require.Equal(t, []string{
		"lib/index.esc",
		"lib/util/strings.esc",
		"bin/main.esc",
	}, files)
}
