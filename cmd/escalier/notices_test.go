package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestThirdPartyNotices_NamesEveryLinkedModule asserts that
// THIRD_PARTY_NOTICES.txt names every module outside this repo that the
// escalier binary links. The platform packages ship the file beside the
// binary, so a new dependency must add its notice there.
func TestThirdPartyNotices_NamesEveryLinkedModule(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps",
		"-f", "{{with .Module}}{{if not .Main}}{{.Path}}{{end}}{{end}}", ".").Output()
	require.NoError(t, err)

	notices, err := os.ReadFile("THIRD_PARTY_NOTICES.txt")
	require.NoError(t, err)

	for _, module := range strings.Fields(string(out)) {
		require.Contains(t, string(notices), module,
			"THIRD_PARTY_NOTICES.txt should carry the notice for %s", module)
	}
}
