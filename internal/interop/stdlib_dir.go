package interop

import (
	"fmt"
	"os"
	"path/filepath"
)

// StdlibDir resolves the on-disk directory containing the stdlib `.esc`
// files (`std/`, `dom/`, `node/` subtrees) shipped with the compiler.
// The path returned is the directory that contains those subtrees, not
// one of the subtrees themselves.
//
// `cliOverride` is the value passed to `--stdlib-dir` on the CLI;
// callers pass "" when the flag was not supplied.
//
// Discovery order, first hit wins:
//
//  1. `cliOverride` (the `--stdlib-dir` CLI flag).
//  2. The `ESCALIER_STDLIB_DIR` environment variable.
//  3. Sibling to the executable: `<exe-dir>/../share/escalier/data/`,
//     falling back to `<exe-dir>/data/` for single-directory installs.
//  4. Repo-relative: walk up from the executable looking for a directory
//     containing `internal/interop/data/`. Makes `go run ./cmd/escalier`
//     work without setup.
//
// Both `cliOverride` and the env var must point at a directory that
// contains a `std/` subdirectory; otherwise the call errors without
// falling through. The sibling and repo-relative paths silently skip
// when the candidate doesn't exist. If nothing resolves, the returned
// error names every discovery channel so the caller can surface a
// fatal startup diagnostic.
func StdlibDir(cliOverride string) (string, error) {
	exe, _ := os.Executable()
	return resolveStdlibDir(stdlibDirInputs{
		cliOverride: cliOverride,
		envVar:      os.Getenv("ESCALIER_STDLIB_DIR"),
		exePath:     exe,
	})
}

// stdlibDirInputs captures the ambient state StdlibDir consults. Pulling
// it into a struct lets tests drive each discovery branch in isolation
// without mutating real env vars or filesystem layout.
type stdlibDirInputs struct {
	cliOverride string
	envVar      string
	exePath     string // result of os.Executable; "" if unavailable
}

func resolveStdlibDir(in stdlibDirInputs) (string, error) {
	if in.cliOverride != "" {
		if !looksLikeStdlibDir(in.cliOverride) {
			return "", fmt.Errorf(
				"--stdlib-dir %q does not contain a std/ subdirectory",
				in.cliOverride,
			)
		}
		return filepath.Clean(in.cliOverride), nil
	}
	if in.envVar != "" {
		if !looksLikeStdlibDir(in.envVar) {
			return "", fmt.Errorf(
				"ESCALIER_STDLIB_DIR=%q does not contain a std/ subdirectory",
				in.envVar,
			)
		}
		return filepath.Clean(in.envVar), nil
	}
	if in.exePath != "" {
		exeDir := filepath.Dir(in.exePath)
		for _, candidate := range []string{
			filepath.Join(exeDir, "..", "share", "escalier", "data"),
			filepath.Join(exeDir, "data"),
		} {
			if looksLikeStdlibDir(candidate) {
				return filepath.Clean(candidate), nil
			}
		}
		if root := findEscalierRoot(exeDir); root != "" {
			candidate := filepath.Join(root, "internal", "interop", "data")
			if looksLikeStdlibDir(candidate) {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf(
		"could not locate Escalier stdlib data directory; pass --stdlib-dir, " +
			"set ESCALIER_STDLIB_DIR, or install share/escalier/data/ next to " +
			"the escalier binary",
	)
}

// SetStdlibDirForTest configures ESCALIER_STDLIB_DIR so callers can
// resolve stdlib imports during tests. The production lookup
// (executable-relative walk) doesn't work in tests because the test
// binary lives in a tmp dir; this helper walks up from the current
// working directory — which `go test` sets to the package source
// dir — to find the repo root.
//
// Call from TestMain in each test package that transitively resolves
// `std:`/`dom:`/`node:` imports.
func SetStdlibDirForTest() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root := findEscalierRoot(cwd)
	if root == "" {
		return fmt.Errorf("could not locate Escalier repo root from %s", cwd)
	}
	return os.Setenv("ESCALIER_STDLIB_DIR", filepath.Join(root, "internal", "interop", "data"))
}

// isStdlibSchemeSubtree reports whether p, relative to root, names a
// top-level scheme subdirectory (`std`, `dom`, `node`) that belongs to
// the builtins workstream rather than the override system. The
// override loader uses this to skip those subtrees while walking the
// shared `internal/interop/data/` directory.
func isStdlibSchemeSubtree(p, root string) bool {
	rel := p
	if root != "" && root != "." {
		// fs.WalkDir paths are joined under root; strip the prefix so
		// "<root>/std" becomes "std".
		if r, ok := stripDirPrefix(rel, root); ok {
			rel = r
		}
	}
	switch rel {
	case "std", "dom", "node":
		return true
	}
	return false
}

func stripDirPrefix(p, prefix string) (string, bool) {
	if p == prefix {
		return "", true
	}
	if len(p) > len(prefix) && p[:len(prefix)] == prefix && p[len(prefix)] == '/' {
		return p[len(prefix)+1:], true
	}
	return "", false
}

// looksLikeStdlibDir reports whether path looks like a stdlib data
// directory — i.e. contains a `std/` subdirectory. The `dom/` and
// `node/` subtrees are not required; either may be absent in a stripped
// distribution.
func looksLikeStdlibDir(path string) bool {
	info, err := os.Stat(filepath.Join(path, "std"))
	return err == nil && info.IsDir()
}
