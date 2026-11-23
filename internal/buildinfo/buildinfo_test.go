package buildinfo_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestVersionFromLdflags asserts that each binary prints the version that
// `-ldflags "-X github.com/escalier-lang/escalier/internal/buildinfo.Version=..."`
// sets. The -X flag names Version by its import path, so the test fails if
// the variable is renamed or moved.
func TestVersionFromLdflags(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binaries")
	}
	tests := []struct {
		name string
		pkg  string
	}{
		{name: "escalier", pkg: "github.com/escalier-lang/escalier/cmd/escalier"},
		{name: "lsp-server", pkg: "github.com/escalier-lang/escalier/cmd/lsp-server"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), test.name)
			if runtime.GOOS == "windows" {
				bin += ".exe"
			}
			ldflags := "-X github.com/escalier-lang/escalier/internal/buildinfo.Version=1.2.3"
			build := exec.Command("go", "build", "-ldflags", ldflags, "-o", bin, test.pkg)
			output, err := build.CombinedOutput()
			require.NoError(t, err, string(output))

			output, err = exec.Command(bin, "--version").Output()
			require.NoError(t, err)
			require.Equal(t, "1.2.3\n", string(output))
		})
	}
}
