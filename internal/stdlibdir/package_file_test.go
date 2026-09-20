package stdlibdir

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPackageName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		basename string
		want     string
		wantOK   bool
	}{
		"no suffix":        {"fetch.esc", "fetch", true},
		"one environment":  {"dom.window.esc", "dom", true},
		"two environments": {"foo.window.service_worker.esc", "foo", true},
		"underscores kept": {"web_rtc.window.esc", "web_rtc", true},
		"not an esc file":  {"dom.window.d.ts", "", false},
		"digest sidecar":   {"dom.digest", "", false},
		"empty name":       {".window.esc", "", false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, ok := PackageName(tt.basename)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

// A package is found under its plain name and under a suffixed one alike, which
// is what lets a package's environments change without its importers moving.
func TestPackageFile_FindsAPackageEitherWay(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "web"), 0o755))
	write(t, dir, "web/fetch.esc")
	write(t, dir, "web/dom.window.esc")

	got, found, err := PackageFile(dir, "web", "fetch")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, filepath.Join(dir, "web", "fetch.esc"), got)

	got, found, err = PackageFile(dir, "web", "dom")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, filepath.Join(dir, "web", "dom.window.esc"), got)
}

// A name that is a prefix of another package's name is not that package.
func TestPackageFile_DoesNotMatchAPrefix(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "web"), 0o755))
	write(t, dir, "web/web_rtc.window.esc")

	_, found, err := PackageFile(dir, "web", "web")
	require.NoError(t, err)
	require.False(t, found)
}

func TestPackageFile_ReportsNoSuchPackage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "web"), 0o755))

	_, found, err := PackageFile(dir, "web", "nonesuch")
	require.NoError(t, err)
	require.False(t, found)
}

// A missing scheme directory is not an error. A tree that ships no `node/`
// subtree answers "no such package" rather than failing every lookup.
func TestPackageFile_TreatsAMissingSchemeDirAsEmpty(t *testing.T) {
	t.Parallel()

	_, found, err := PackageFile(t.TempDir(), "node", "fs")
	require.NoError(t, err)
	require.False(t, found)
}

// Two suffixed files claiming one package is an error, since nothing says which
// of them the import meant. An unsuffixed file wins outright, so it is not part
// of this case.
func TestPackageFile_RejectsTwoSuffixedFilesForOnePackage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "web"), 0o755))
	write(t, dir, "web/dom.window.esc")
	write(t, dir, "web/dom.worker.esc")

	_, _, err := PackageFile(dir, "web", "dom")
	require.Error(t, err)
	require.Equal(t,
		"web:dom is declared by both dom.window.esc and dom.worker.esc under "+
			filepath.Join(dir, "web"),
		err.Error())
}

func write(t *testing.T, dir, rel string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte("\n"), 0o644))
}
