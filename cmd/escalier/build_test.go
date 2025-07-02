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

func checkFile(t *testing.T, fixtureDir string, ext string) {
	outPath := filepath.Join("dist", "index"+ext)
	actualJs, err := os.ReadFile(outPath)
	require.NoError(t, err)

	if os.Getenv("UPDATE_FIXTURES") == "true" {
		err = os.WriteFile(filepath.Join(fixtureDir, outPath), actualJs, 0644)
		require.NoError(t, err)
	} else {
		expectedJs, err := os.ReadFile(filepath.Join(fixtureDir, outPath))
		require.NoError(t, err)
		require.Equal(t, string(expectedJs), string(actualJs))
	}
}

// TODO: print errors to a file
func checkFixture(t *testing.T, fixtureDir string, fixtureName string) {
	tmpDir := t.TempDir()

	err := os.Chdir(tmpDir)
	require.NoError(t, err)

	srcFile := filepath.Join(fixtureDir, fixtureName+".esc")
	destFile := filepath.Join(tmpDir, fixtureName+".esc")

	input, err := os.ReadFile(srcFile)
	require.NoError(t, err)

	err = os.WriteFile(destFile, input, 0644)
	require.NoError(t, err)

	stdout := bytes.NewBuffer(nil)
	stderr := bytes.NewBuffer(nil)

	// TODO: find all .esc files in the fixture directory
	// and pass them to the build function.
	build(stdout, stderr, []string{fixtureName + ".esc"})
	fmt.Println("stderr =", stderr.String())

	// create dist/ directory if it doesn't exist
	if _, err := os.Stat(filepath.Join(fixtureDir, "dist")); os.IsNotExist(err) {
		err := os.Mkdir(filepath.Join(fixtureDir, "dist"), 0755)
		if err != nil {
			fmt.Fprintln(stderr, "failed to create dist directory")
		}
	}

	checkFile(t, fixtureDir, ".js")
	checkFile(t, fixtureDir, ".d.ts")
	checkFile(t, fixtureDir, ".esc.map")

	// check errors
	if stderr.Len() > 0 {
		actualErr := stderr.Bytes()
		if os.Getenv("UPDATE_FIXTURES") == "true" {
			err = os.WriteFile(filepath.Join(fixtureDir, "error.txt"), actualErr, 0644)
			require.NoError(t, err)
		} else {
			expectedErr, err := os.ReadFile(filepath.Join(fixtureDir, "error.txt"))
			require.NoError(t, err)
			require.Equal(t, string(expectedErr), string(actualErr))
		}
	}

	// if there are no errors, check that the error file does not exist
	if stderr.Len() == 0 {
		if os.Getenv("UPDATE_FIXTURES") == "true" {
			// remove the error file if it exists
			if _, err := os.Stat(filepath.Join(fixtureDir, "error.txt")); !os.IsNotExist(err) {
				// file exists, remove it
				err = os.Remove(filepath.Join(fixtureDir, "error.txt"))
				require.NoError(t, err)
			}
		} else {
			// check that the error file does not exist
			_, err := os.Stat(filepath.Join(fixtureDir, "error.txt"))
			require.True(t, os.IsNotExist(err), "error file should not exist")
		}
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
			if group.Name() == "extractors" {
				continue
			}
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
