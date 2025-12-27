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

// findRepoRoot walks up the directory tree to find the repository root
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		// Check if go.mod exists in current directory
		goModPath := filepath.Join(dir, "go.mod")
		_, err := os.Lstat(goModPath)
		if err == nil {
			return dir, nil
		}

		// Move up one directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the root without finding go.mod
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

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
func checkFixture(t *testing.T, repoRoot string, fixtureDir string) {
	tmpDir := t.TempDir()

	err := os.Mkdir(filepath.Join(tmpDir, "lib"), 0755)
	require.NoError(t, err)

	// Create a symlink named "node_modules" in the temporary directory that
	// points to the repository's node_modules folder.
	nodeModulesTarget := filepath.Join(repoRoot, "node_modules")
	fmt.Printf("nodeModulesTarget = %s\n", nodeModulesTarget)
	nodeModulesLink := filepath.Join(tmpDir, "node_modules")
	err = os.Symlink(nodeModulesTarget, nodeModulesLink)
	if err != nil && !os.IsExist(err) {
		require.NoError(t, err, "failed to create node_modules symlink")
	}

	// Create a symlink for go.mod in the temporary directory that points
	// to the repository's go.mod file.
	// This is necessary because the prelude looks for the go.mod file to
	// determine the root of the repository.
	goModTarget := filepath.Join(repoRoot, "go.mod")
	goModLink := filepath.Join(tmpDir, "go.mod")
	err = os.Symlink(goModTarget, goModLink)
	if err != nil && !os.IsExist(err) {
		require.NoError(t, err, "failed to create go.mod symlink")
	}

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

	// Write errors to error.txt if there are any
	if stderr.Len() > 0 {
		err = os.WriteFile(filepath.Join(tmpDir, "error.txt"), stderr.Bytes(), 0644)
		require.NoError(t, err, "failed to write error.txt")
	}

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

		// Copy error.txt if it exists
		errorTxtPath := filepath.Join(tmpDir, "error.txt")
		if _, err := os.Stat(errorTxtPath); err == nil {
			errorContent, err := os.ReadFile(errorTxtPath)
			if err != nil {
				fmt.Fprintln(stderr, "failed to read error.txt:", err)
				return
			}
			err = os.WriteFile(filepath.Join(fixtureDir, "error.txt"), errorContent, 0644)
			if err != nil {
				fmt.Fprintln(stderr, "failed to write error.txt to fixture:", err)
				return
			}
		} else {
			// Remove error.txt from fixture if it exists but there are no errors
			_ = os.Remove(filepath.Join(fixtureDir, "error.txt"))
		}
	} else {
		// Compare error.txt if it exists
		errorTxtPath := filepath.Join(tmpDir, "error.txt")
		expectedErrorTxtPath := filepath.Join(fixtureDir, "error.txt")

		_, errorExists := os.Stat(errorTxtPath)
		_, expectedErrorExists := os.Stat(expectedErrorTxtPath)

		if errorExists == nil && expectedErrorExists == nil {
			// Both exist, compare them
			actualErrorContent, err := os.ReadFile(errorTxtPath)
			require.NoError(t, err, "failed to read generated error.txt")

			expectedErrorContent, err := os.ReadFile(expectedErrorTxtPath)
			require.NoError(t, err, "failed to read expected error.txt")

			require.Equal(t, string(expectedErrorContent), string(actualErrorContent), "error.txt contents should match")
		} else if errorExists == nil && expectedErrorExists != nil {
			// error.txt was generated but not expected
			t.Errorf("error.txt was generated but not expected in fixture")
		} else if errorExists != nil && expectedErrorExists == nil {
			// error.txt was expected but not generated
			t.Errorf("error.txt was expected but not generated")
		}
		// If neither exists, that's fine - no errors expected or generated

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
	repoRoot, err := findRepoRoot()
	require.NoError(t, err, "failed to find repository root")

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
			checkFixture(t, repoRoot, fixtureDir)
		})
	}
}
