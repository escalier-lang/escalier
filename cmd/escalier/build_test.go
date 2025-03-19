package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuild(t *testing.T) {
	tmpDir := t.TempDir()

	err := os.Chdir(tmpDir)
	require.NoError(t, err)
	err = os.Mkdir("fixtures", 0755)
	require.NoError(t, err)

	_, currentFile, _, _ := runtime.Caller(0)
	rootDir := filepath.Join(filepath.Dir(currentFile), "..", "..")

	srcFile := filepath.Join(rootDir, "fixtures/test.esc")
	destFile := filepath.Join(tmpDir, "fixtures/test.esc")

	input, err := os.ReadFile(srcFile)
	require.NoError(t, err)

	err = os.WriteFile(destFile, input, 0644)
	require.NoError(t, err)

	out := bytes.NewBuffer(nil)
	build(out, []string{"./fixtures/test.esc"})

	stdout := out.String()

	fmt.Println("stdout =", stdout)

	_, err = os.Stat("fixtures/test.js")
	require.NoError(t, err)

	expectedJs, err := os.ReadFile(filepath.Join(rootDir, "fixtures/test.js"))
	require.NoError(t, err)

	actualJs, err := os.ReadFile("fixtures/test.js")
	require.NoError(t, err)

	require.Equal(t, string(expectedJs), string(actualJs))

	expectedMap, err := os.ReadFile(filepath.Join(rootDir, "fixtures/test.esc.map"))
	require.NoError(t, err)

	actualMap, err := os.ReadFile("fixtures/test.esc.map")
	require.NoError(t, err)

	require.Equal(t, string(expectedMap), string(actualMap))
}
