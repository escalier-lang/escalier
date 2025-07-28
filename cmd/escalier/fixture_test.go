package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func checkFile(t *testing.T, fixtureDir string, ext string) {
	outPath := filepath.Join("build", "lib", "index"+ext)
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
func checkFixture(t *testing.T, fixtureDir string) {
	tmpDir := t.TempDir()

	err := os.Mkdir(filepath.Join(tmpDir, "lib"), 0755)
	require.NoError(t, err)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	// find all .esc files in the fixture directory and copy them over to the tmpDir
	// maintaining the directory structure
	fixtureLibDir := filepath.Join(fixtureDir, "lib")
	err = filepath.WalkDir(fixtureLibDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Get the relative path from the fixture lib directory
		relPath, err := filepath.Rel(fixtureLibDir, path)
		if err != nil {
			return err
		}

		destPath := filepath.Join(tmpDir, "lib", relPath)

		if d.IsDir() {
			// Create directory in destination
			return os.MkdirAll(destPath, 0755)
		}

		// Check if it's a file and ends with .esc
		if strings.HasSuffix(d.Name(), ".esc") {
			// Ensure the destination directory exists
			destDir := filepath.Dir(destPath)
			if err := os.MkdirAll(destDir, 0755); err != nil {
				return err
			}

			input, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			err = os.WriteFile(destPath, input, 0644)
			if err != nil {
				return err
			}
		}

		return nil
	})
	require.NoError(t, err)

	stdout := bytes.NewBuffer(nil)
	stderr := bytes.NewBuffer(nil)

	// Find all .esc files in the lib directory
	var files []string
	err = filepath.WalkDir("lib", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Check if it's a file and ends with .esc
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".esc") {
			files = append(files, path)
		}

		return nil
	})

	if err != nil {
		fmt.Fprintln(stderr, "failed to walk directory:", err)
		return
	}

	build(stdout, stderr, files)
	fmt.Println("stderr =", stderr.String())

	// create build/ directory if it doesn't exist
	if _, err := os.Stat(filepath.Join(fixtureDir, "build")); os.IsNotExist(err) {
		err := os.Mkdir(filepath.Join(fixtureDir, "build"), 0755)
		if err != nil {
			fmt.Fprintln(stderr, "failed to create build directory")
		}
		if _, err := os.Stat(filepath.Join(fixtureDir, "build", "lib")); os.IsNotExist(err) {
			err := os.Mkdir(filepath.Join(fixtureDir, "build", "lib"), 0755)
			if err != nil {
				fmt.Fprintln(stderr, "failed to create build/lib directory")
			}
		}
	}

	checkFile(t, fixtureDir, ".js")
	checkFile(t, fixtureDir, ".d.ts")
	checkFile(t, fixtureDir, ".js.map")

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

func TestBuildFixtureTests(t *testing.T) {
	origDir, err := os.Getwd()
	require.NoError(t, err)
	defer func() {
		_ = os.Chdir(origDir)
	}()

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
			if group.Name() != "basics" && fixture.Name() != "literals" {
				continue
			}
			name := group.Name() + "/" + fixture.Name()
			t.Run(name, func(t *testing.T) {
				fixtureDir := filepath.Join(rootDir, "fixtures", group.Name(), fixture.Name())
				checkFixture(t, fixtureDir)
			})
		}
	}
}
