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

// TODO: print errors to a file
func checkFixture(t *testing.T, fixtureDir string, fixtureName string) {
	tmpDir := t.TempDir()
	shouldUpdate := os.Getenv("UPDATE_FIXTURES") == "true"

	err := os.Chdir(tmpDir)
	require.NoError(t, err)

	srcFile := filepath.Join(fixtureDir, fixtureName+".esc")
	destFile := filepath.Join(tmpDir, fixtureName+".esc")

	input, err := os.ReadFile(srcFile)
	require.NoError(t, err)

	err = os.WriteFile(destFile, input, 0644)
	require.NoError(t, err)

	out := bytes.NewBuffer(nil)
	build(out, []string{fixtureName + ".esc"})
	fmt.Println("stdout =", out.String())

	actualJs, err := os.ReadFile(fixtureName + ".js")
	require.NoError(t, err)

	if shouldUpdate {
		err = os.WriteFile(filepath.Join(fixtureDir, fixtureName+".js"), actualJs, 0644)
		require.NoError(t, err)
	} else {
		expectedJs, err := os.ReadFile(filepath.Join(fixtureDir, fixtureName+".js"))
		require.NoError(t, err)
		require.Equal(t, string(expectedJs), string(actualJs))
	}

	actualMap, err := os.ReadFile(fixtureName + ".esc.map")
	require.NoError(t, err)

	if shouldUpdate {
		err = os.WriteFile(filepath.Join(fixtureDir, fixtureName+".esc.map"), actualMap, 0644)
		require.NoError(t, err)
	} else {
		expectedMap, err := os.ReadFile(filepath.Join(fixtureDir, fixtureName+".esc.map"))
		require.NoError(t, err)
		require.Equal(t, string(expectedMap), string(actualMap))
	}
}

func TestBuild(t *testing.T) {
	_, currentFile, _, _ := runtime.Caller(0)
	rootDir := filepath.Join(filepath.Dir(currentFile), "..", "..")

	groups, err := os.ReadDir(filepath.Join(rootDir, "fixtures"))
	require.NoError(t, err)

	for _, group := range groups {
		if !group.IsDir() {
			continue
		}

		fixtures, err := os.ReadDir(filepath.Join(rootDir, "fixtures", group.Name()))
		require.NoError(t, err)

		for _, fixture := range fixtures {
			// TODO: use an environment variable for this instead
			// if fixture.Name() != "destructuring" {
			// 	continue
			// }
			name := group.Name() + "/" + fixture.Name()
			t.Run(name, func(t *testing.T) {
				fixtureDir := filepath.Join(rootDir, "fixtures", group.Name(), fixture.Name())
				fixtureName := fixture.Name()
				checkFixture(
					t,
					fixtureDir,
					fixtureName,
				)
			})
		}
	}
}
