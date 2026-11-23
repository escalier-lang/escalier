package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWriteVersion asserts that `escalier --version` prints the default
// version of a build that doesn't set it with -ldflags.
func TestWriteVersion(t *testing.T) {
	var out bytes.Buffer
	writeVersion(&out)
	require.Equal(t, "dev\n", out.String())
}
