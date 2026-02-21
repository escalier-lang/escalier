package resolver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveTypesPackage finds the @types package for a given module name.
// Returns the path to the package directory, or an error if not found.
//
// Resolution algorithm:
// 1. Starting from fromDir, look for node_modules/@types/moduleName
// 2. Walk up parent directories, checking each node_modules/@types/moduleName
// 3. Stop when found or when reaching the filesystem root
func ResolveTypesPackage(moduleName string, fromDir string) (string, error) {
	typesPackage := "@types/" + moduleName

	dir := fromDir
	for {
		candidate := filepath.Join(dir, "node_modules", typesPackage)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root
			return "", fmt.Errorf("@types/%s not found", moduleName)
		}
		dir = parent
	}
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
