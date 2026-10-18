package resolver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveTypesEntryPoint returns the `.d.ts` file the types for moduleName start at.
// It looks in the `node_modules` directories at and above fromDir, the way Node looks
// a package up, and in each one tries in turn:
//
//  1. `node_modules/<moduleName>`, a package that ships its own types;
//  2. `node_modules/@types/<name>`, a package whose types are published apart. A
//     scoped `@scope/pkg` publishes them as `@types/scope__pkg`.
//
// A subpath such as `lodash/fp` resolves to the directory of that name inside the
// package, or to a `.d.ts` file of that name. A moduleName with a `.` or `..`
// segment names no package and is rejected. When nothing holds types, the error is
// GetTypesEntryPoint's for the first package directory that exists but has no entry
// point. With no such directory, it names every directory the lookup tried.
func ResolveTypesEntryPoint(moduleName string, fromDir string) (string, error) {
	return resolveTypes(moduleName, fromDir, false)
}

// ResolveTypeReference is ResolveTypesEntryPoint for the target of a
// `/// <reference types="…" />` directive, which tries `node_modules/@types/<name>`
// before `node_modules/<name>` in each directory.
func ResolveTypeReference(name string, fromDir string) (string, error) {
	return resolveTypes(name, fromDir, true)
}

// resolveTypes is ResolveTypesEntryPoint, trying the `@types` package first in each
// directory when typesFirst is set.
func resolveTypes(moduleName string, fromDir string, typesFirst bool) (string, error) {
	for _, segment := range strings.Split(moduleName, "/") {
		if segment == "." || segment == ".." || segment == "" {
			return "", fmt.Errorf("%q is a relative path, not a package name", moduleName)
		}
	}
	var tried []string
	var entryErr error
	for dir := fromDir; ; {
		nodeModules := filepath.Join(dir, "node_modules")
		candidates := []string{
			filepath.Join(nodeModules, filepath.FromSlash(moduleName)),
			filepath.Join(nodeModules, "@types", filepath.FromSlash(typesPackageName(moduleName))),
		}
		if typesFirst {
			candidates[0], candidates[1] = candidates[1], candidates[0]
		}
		for _, pkgDir := range candidates {
			tried = append(tried, pkgDir)
			if isDir(pkgDir) {
				path, err := GetTypesEntryPoint(pkgDir)
				if err == nil {
					return path, nil
				}
				// A subpath such as `react/jsx-runtime` can name a file beside a
				// directory of the same name.
				if file := pkgDir + ".d.ts"; isFile(file) {
					return file, nil
				}
				// The lookup goes on, since `node_modules/react` holds only
				// JavaScript and its types are in `node_modules/@types/react`.
				if entryErr == nil {
					entryErr = err
				}
				continue
			}
			if file := pkgDir + ".d.ts"; isFile(file) {
				return file, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if entryErr != nil {
		return "", entryErr
	}
	return "", fmt.Errorf("no types found for %q; looked in %s", moduleName, strings.Join(tried, ", "))
}

// typesPackageName returns the `@types` package name for moduleName. A scoped package
// `@scope/name` publishes its types as `@types/scope__name`, and any subpath after the
// package name is kept.
func typesPackageName(moduleName string) string {
	if !strings.HasPrefix(moduleName, "@") {
		return moduleName
	}
	scope, rest, found := strings.Cut(moduleName[1:], "/")
	if !found {
		return moduleName
	}
	return scope + "__" + rest
}

// isDir reports whether path names a directory, following a symlink.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// isFile reports whether path names a regular file, following a symlink.
func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// GetTypesEntryPoint returns the main .d.ts file for a types package.
// Returns an error if the resolved entry point file does not exist.
//
// Resolution priority:
// 1. exports["types"] or exports["."]["types"] (modern package.json exports)
// 2. "types" field in package.json
// 3. "typings" field in package.json (older convention)
// 4. "main" field with extension replaced by .d.ts
// 5. Fallback to index.d.ts
func GetTypesEntryPoint(packageDir string) (string, error) {
	pkgJsonPath := filepath.Join(packageDir, "package.json")

	data, err := os.ReadFile(pkgJsonPath)
	if err != nil {
		// No package.json, try index.d.ts
		indexPath := filepath.Join(packageDir, "index.d.ts")
		if _, statErr := os.Stat(indexPath); statErr != nil {
			return "", fmt.Errorf("types entry point not found: %s", indexPath)
		}
		return indexPath, nil
	}

	var pkg struct {
		Types   string `json:"types"`
		Typings string `json:"typings"`
		Main    string `json:"main"`
		Exports any    `json:"exports"` // Can be string, object, or nested
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", fmt.Errorf("failed to parse package.json: %w", err)
	}

	// Priority: exports["types"] > exports["."]["types"] > types > typings > main > index.d.ts

	// Check exports field for types (handles modern package.json exports)
	if pkg.Exports != nil {
		if typesPath := resolveExportsTypes(pkg.Exports); typesPath != "" {
			fullPath := filepath.Join(packageDir, typesPath)
			if _, statErr := os.Stat(fullPath); statErr != nil {
				return "", fmt.Errorf("types entry point from exports not found: %s", fullPath)
			}
			return fullPath, nil
		}
	}

	if pkg.Types != "" {
		fullPath := filepath.Join(packageDir, pkg.Types)
		if _, statErr := os.Stat(fullPath); statErr != nil {
			return "", fmt.Errorf("types entry point not found: %s (from 'types' field)", fullPath)
		}
		return fullPath, nil
	}

	if pkg.Typings != "" {
		fullPath := filepath.Join(packageDir, pkg.Typings)
		if _, statErr := os.Stat(fullPath); statErr != nil {
			return "", fmt.Errorf("types entry point not found: %s (from 'typings' field)", fullPath)
		}
		return fullPath, nil
	}

	if pkg.Main != "" {
		// Strip any JS-type extension (.js, .cjs, .mjs) before appending .d.ts
		mainWithoutExt := pkg.Main
		for _, ext := range []string{".mjs", ".cjs", ".js"} {
			if cut, found := strings.CutSuffix(mainWithoutExt, ext); found {
				mainWithoutExt = cut
				break
			}
		}
		dtsMain := mainWithoutExt + ".d.ts"
		fullPath := filepath.Join(packageDir, dtsMain)
		if _, statErr := os.Stat(fullPath); statErr != nil {
			return "", fmt.Errorf("types entry point not found: %s (derived from 'main' field)", fullPath)
		}
		return fullPath, nil
	}

	// Fallback to index.d.ts
	indexPath := filepath.Join(packageDir, "index.d.ts")
	if _, statErr := os.Stat(indexPath); statErr != nil {
		return "", fmt.Errorf("types entry point not found: %s (fallback)", indexPath)
	}
	return indexPath, nil
}

// resolveExportsTypes extracts the types path from package.json exports field.
// Handles various shapes:
//   - exports: "./index.d.ts" (string - if ends in .d.ts)
//   - exports: { "types": "./index.d.ts" }
//   - exports: { ".": { "types": "./index.d.ts" } }
//   - exports: { ".": { "import": { "types": "./index.d.ts" }, "require": { "types": "./index.d.ts" } } }
func resolveExportsTypes(exports any) string {
	switch e := exports.(type) {
	case string:
		// Direct string export - only use if it's a .d.ts file
		if strings.HasSuffix(e, ".d.ts") {
			return e
		}
		return ""
	case map[string]any:
		// Check for direct "types" key
		if types, ok := e["types"].(string); ok {
			return types
		}
		// Check for "." entry (main entry point)
		if dot, ok := e["."]; ok {
			return resolveExportsTypes(dot)
		}
		// Check for nested condition maps (import/require/default)
		for _, key := range []string{"import", "require", "default"} {
			if nested, ok := e[key]; ok {
				if result := resolveExportsTypes(nested); result != "" {
					return result
				}
			}
		}
	}
	return ""
}
