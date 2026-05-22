package interop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// makeFakeStdlib creates a directory tree that looksLikeStdlibDir
// accepts (contains a `std/` subdirectory). Returns the root path.
func makeFakeStdlib(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "std"), 0o755))
	return root
}

func TestResolveStdlibDir_CLIOverride(t *testing.T) {
	dir := makeFakeStdlib(t)
	got, err := resolveStdlibDir(stdlibDirInputs{cliOverride: dir})
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(dir), got)
}

func TestResolveStdlibDir_CLIOverrideRejectsBadPath(t *testing.T) {
	bad := t.TempDir() // empty dir — no std/ subtree
	_, err := resolveStdlibDir(stdlibDirInputs{cliOverride: bad})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--stdlib-dir")
}

func TestResolveStdlibDir_CLIOverrideBeatsEnv(t *testing.T) {
	cli := makeFakeStdlib(t)
	env := makeFakeStdlib(t)
	got, err := resolveStdlibDir(stdlibDirInputs{cliOverride: cli, envVar: env})
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(cli), got)
}

func TestResolveStdlibDir_EnvVar(t *testing.T) {
	dir := makeFakeStdlib(t)
	got, err := resolveStdlibDir(stdlibDirInputs{envVar: dir})
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(dir), got)
}

func TestResolveStdlibDir_EnvVarRejectsBadPath(t *testing.T) {
	bad := t.TempDir()
	_, err := resolveStdlibDir(stdlibDirInputs{envVar: bad})
	require.Error(t, err)
	require.Contains(t, err.Error(), "ESCALIER_STDLIB_DIR")
}

func TestResolveStdlibDir_SiblingShareLayout(t *testing.T) {
	// Simulate a `bin/` + `share/escalier/data/` install layout.
	install := t.TempDir()
	binDir := filepath.Join(install, "bin")
	share := filepath.Join(install, "share", "escalier", "data")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(share, "std"), 0o755))
	fakeExe := filepath.Join(binDir, "escalier")
	require.NoError(t, os.WriteFile(fakeExe, []byte{}, 0o755))

	got, err := resolveStdlibDir(stdlibDirInputs{exePath: fakeExe})
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(share), got)
}

func TestResolveStdlibDir_SiblingSingleDirLayout(t *testing.T) {
	// `<exe-dir>/data/` fallback for single-directory installs.
	install := t.TempDir()
	data := filepath.Join(install, "data")
	require.NoError(t, os.MkdirAll(filepath.Join(data, "std"), 0o755))
	fakeExe := filepath.Join(install, "escalier")
	require.NoError(t, os.WriteFile(fakeExe, []byte{}, 0o755))

	got, err := resolveStdlibDir(stdlibDirInputs{exePath: fakeExe})
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(data), got)
}

func TestResolveStdlibDir_RepoRelative(t *testing.T) {
	// Simulate the build-tree layout: a fake repo root containing
	// `internal/interop/data/std/`, with the executable nested
	// somewhere inside (mimicking `go run`'s tmp-built binary).
	root := t.TempDir()
	data := filepath.Join(root, "internal", "interop", "data")
	require.NoError(t, os.MkdirAll(filepath.Join(data, "std"), 0o755))
	exeDir := filepath.Join(root, "some", "nested", "tmp")
	require.NoError(t, os.MkdirAll(exeDir, 0o755))
	fakeExe := filepath.Join(exeDir, "escalier")
	require.NoError(t, os.WriteFile(fakeExe, []byte{}, 0o755))

	got, err := resolveStdlibDir(stdlibDirInputs{exePath: fakeExe})
	require.NoError(t, err)
	require.Equal(t, data, got)
}

func TestResolveStdlibDir_NoneResolveFatalError(t *testing.T) {
	// Empty exe dir, no env, no override, no candidate found.
	emptyDir := t.TempDir()
	fakeExe := filepath.Join(emptyDir, "escalier")
	require.NoError(t, os.WriteFile(fakeExe, []byte{}, 0o755))

	_, err := resolveStdlibDir(stdlibDirInputs{exePath: fakeExe})
	require.Error(t, err)
	require.Contains(t, err.Error(), "could not locate Escalier stdlib data directory")
	require.Contains(t, err.Error(), "ESCALIER_STDLIB_DIR")
}

func TestSetStdlibDirForTest_ResolvesRealRepoData(t *testing.T) {
	// SetStdlibDirForTest is what other test packages call from TestMain
	// to point ESCALIER_STDLIB_DIR at the in-repo `internal/interop/data/`
	// tree. Verify it lands on a directory containing std/.
	t.Setenv("ESCALIER_STDLIB_DIR", "")
	require.NoError(t, SetStdlibDirForTest())
	dir := os.Getenv("ESCALIER_STDLIB_DIR")
	require.NotEmpty(t, dir)
	require.Contains(t, dir, filepath.Join("internal", "interop", "data"))
	info, err := os.Stat(filepath.Join(dir, "std"))
	require.NoError(t, err)
	require.True(t, info.IsDir())

	// And the public entry point should agree.
	got, err := StdlibDir("")
	require.NoError(t, err)
	require.Equal(t, dir, got)
}
