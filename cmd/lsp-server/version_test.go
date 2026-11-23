package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWriteVersion asserts that `lsp-server --version` prints the default
// version of a build that doesn't set it with -ldflags.
func TestWriteVersion(t *testing.T) {
	var out bytes.Buffer
	writeVersion(&out)
	require.Equal(t, "dev\n", out.String())
}

// TestServerInfo asserts that the `initialize` response names the server
// and reports the same version as --version.
func TestServerInfo(t *testing.T) {
	info := serverInfo()
	require.Equal(t, "escalier", info.Name)
	require.NotNil(t, info.Version)
	require.Equal(t, "dev", *info.Version)
}
