package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func copyDir(src string, dst string) error {
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Get the relative path from the fixture lib directory
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		destPath := filepath.Join(dst, relPath)

		if d.IsDir() {
			// Create directory in destination
			return os.MkdirAll(destPath, 0755)
		}

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

		return nil
	})

	return err
}

// TODO: Update this to work with changes to `build` in build.go
func checkFixture(t *testing.T, fixtureDir string) {
	tmpDir := t.TempDir()

	err := os.Mkdir(filepath.Join(tmpDir, "lib"), 0755)
	require.NoError(t, err)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	// Copy the fixture's lib and bin directories over to the temp directory
	_, err = os.Stat(filepath.Join(fixtureDir, "lib"))
	if !os.IsNotExist(err) {
		err = copyDir(filepath.Join(fixtureDir, "lib"), filepath.Join(tmpDir, "lib"))
		require.NoError(t, err)
	}

	_, err = os.Stat(filepath.Join(fixtureDir, "bin"))
	if !os.IsNotExist(err) {
		err = copyDir(filepath.Join(fixtureDir, "bin"), filepath.Join(tmpDir, "bin"))
		require.NoError(t, err)
	}

	// files, err := compiler.FindSourceFiles()
	// require.NoError(t, err)

	stdout := bytes.NewBuffer(nil)
	stderr := bytes.NewBuffer(nil)
	build(stdout, stderr, []string{"."})
	fmt.Println("stderr =", stderr.String())

	if os.Getenv("UPDATE_FIXTURES") == "true" {
		err = os.RemoveAll(filepath.Join(fixtureDir, "build"))
		if err != nil {
			fmt.Fprintln(stderr, "failed to remove build directory:", err)
			return
		}

		err = copyDir(filepath.Join(tmpDir, "build"), filepath.Join(fixtureDir, "build"))
		if err != nil {
			fmt.Fprintln(stderr, "failed to copy build directory:", err)
			return
		}
	} else {
		// Check if all of the files are the same
		buildDir := filepath.Join(tmpDir, "build")
		err = filepath.WalkDir(buildDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			// Skip the build directory itself
			if path == buildDir {
				return nil
			}

			// Get the relative path from the build directory
			relPath, err := filepath.Rel(buildDir, path)
			if err != nil {
				return err
			}

			expectedPath := filepath.Join(fixtureDir, "build", relPath)

			if d.IsDir() {
				// Check that the directory exists in the fixture
				_, err := os.Stat(expectedPath)
				require.NoError(t, err, "directory %s should exist in fixture", relPath)
			} else {
				// Compare file contents
				actualContent, err := os.ReadFile(path)
				require.NoError(t, err, "failed to read generated file %s", relPath)

				expectedContent, err := os.ReadFile(expectedPath)
				require.NoError(t, err, "expected file %s should exist in fixture", relPath)

				require.Equal(t, string(expectedContent), string(actualContent), "file %s contents should match", relPath)
			}

			return nil
		})
		require.NoError(t, err)
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

	fixtures, err := os.ReadDir(filepath.Join(rootDir, "fixtures"))
	require.NoError(t, err)

	for _, fixture := range fixtures {
		t.Run(fixture.Name(), func(t *testing.T) {
			fixtureDir := filepath.Join(rootDir, "fixtures", fixture.Name())
			checkFixture(t, fixtureDir)
		})
	}
}
