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
// Each package directory is read as packageTypesFile describes, so a subpath such as
// `lodash/fp` goes through the package's `exports` and `typesVersions` before the
// directory or `.d.ts` file of that name. A moduleName with a `.` or `..` segment
// names no package and is rejected. When nothing holds types, the error is the first
// one a package directory that exists reported. With no such error, it names every
// path the lookup tried.
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
	if err := ValidateModuleName(moduleName); err != nil {
		return "", err
	}
	name := PackageName(moduleName)
	subpath := strings.TrimPrefix(strings.TrimPrefix(moduleName, name), "/")
	var tried []string
	var entryErr error
	for dir := fromDir; ; {
		nodeModules := filepath.Join(dir, "node_modules")
		candidates := []string{
			filepath.Join(nodeModules, filepath.FromSlash(name)),
			filepath.Join(nodeModules, "@types", filepath.FromSlash(TypesPackageName(name))),
		}
		if typesFirst {
			candidates[0], candidates[1] = candidates[1], candidates[0]
		}
		for _, pkgDir := range candidates {
			tried = append(tried, joinRel(pkgDir, subpath))
			if !isDir(pkgDir) {
				// A package of one file sits beside the directory it would have had.
				if file := pkgDir + ".d.ts"; subpath == "" && isFile(file) {
					return file, nil
				}
				continue
			}
			path, err := packageTypesFile(pkgDir, subpath)
			if path != "" {
				return path, nil
			}
			// The lookup goes on, since `node_modules/react` holds only
			// JavaScript and its types are in `node_modules/@types/react`.
			if err != nil && entryErr == nil {
				entryErr = err
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

// TypesPackageName returns the `@types` package name for moduleName. A scoped package
// `@scope/name` publishes its types as `@types/scope__name`, and any subpath after the
// package name is kept.
func TypesPackageName(moduleName string) string {
	if !strings.HasPrefix(moduleName, "@") {
		return moduleName
	}
	scope, rest, found := strings.Cut(moduleName[1:], "/")
	if !found {
		return moduleName
	}
	return scope + "__" + rest
}

// ValidateModuleName returns an error when moduleName has an empty, `.` or `..`
// segment, which makes it a path rather than a package name.
func ValidateModuleName(moduleName string) error {
	for _, segment := range strings.Split(moduleName, "/") {
		if segment == "." || segment == ".." || segment == "" {
			return fmt.Errorf("%q is a relative path, not a package name", moduleName)
		}
	}
	return nil
}

// PackageName returns the package a module specifier names, leaving out any subpath.
// `react/jsx-runtime` names `react`, and `@scope/pkg/sub` names `@scope/pkg`.
func PackageName(moduleName string) string {
	segments := 1
	if strings.HasPrefix(moduleName, "@") {
		segments = 2
	}
	parts := strings.SplitN(moduleName, "/", segments+1)
	if len(parts) > segments {
		parts = parts[:segments]
	}
	return strings.Join(parts, "/")
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

// GetTypesEntryPoint returns the `.d.ts` file the types of the package in packageDir
// start at. It returns an error when the package names an entry point that does not
// exist, or names none and holds no `index.d.ts`.
//
// It reads the package.json beside it in this order, using the first that applies:
//
//  1. the `exports` entry for `.`, read as exportsTarget describes;
//  2. the `types` field;
//  3. the `typings` field, an older name for `types`;
//  4. the `main` field, with its JavaScript extension replaced by `.d.ts`;
//  5. `index.d.ts`.
//
// A `typesVersions` field redirects the file the last four name.
func GetTypesEntryPoint(packageDir string) (string, error) {
	return packageTypesFile(packageDir, "")
}

// packageManifest holds the package.json fields the resolver reads.
type packageManifest struct {
	// found is false when the package has no package.json.
	found         bool
	types         string
	typings       string
	main          string
	exports       *jsonNode
	typesVersions *jsonNode
}

// readManifest reads the package.json in pkgDir. A directory with none gives a
// manifest whose found is false.
func readManifest(pkgDir string) (packageManifest, error) {
	data, err := os.ReadFile(filepath.Join(pkgDir, "package.json"))
	if err != nil {
		return packageManifest{}, nil
	}
	var fields struct {
		Types         string          `json:"types"`
		Typings       string          `json:"typings"`
		Main          string          `json:"main"`
		Exports       json.RawMessage `json:"exports"`
		TypesVersions json.RawMessage `json:"typesVersions"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return packageManifest{}, fmt.Errorf("failed to parse package.json: %w", err)
	}
	manifest := packageManifest{found: true, types: fields.Types, typings: fields.Typings, main: fields.Main}
	if len(fields.Exports) > 0 {
		if manifest.exports, err = decodeOrdered(fields.Exports); err != nil {
			return packageManifest{}, fmt.Errorf("failed to parse package.json: %w", err)
		}
	}
	if len(fields.TypesVersions) > 0 {
		if manifest.typesVersions, err = decodeOrdered(fields.TypesVersions); err != nil {
			return packageManifest{}, fmt.Errorf("failed to parse package.json: %w", err)
		}
	}
	return manifest, nil
}

// packageTypesFile returns the `.d.ts` file the types for subpath of the package in
// pkgDir start at. subpath is "" for the package itself, or a path inside it such as
// `jsx-runtime`.
//
// A package.json with an `exports` field decides first. A subpath it does not list is
// an error, since Node and TypeScript refuse to import one. An entry that leads only to
// JavaScript files leaves the lookup to the fields GetTypesEntryPoint lists for the
// package itself, and to typesVersionsFile then subpathFile for a subpath. It returns
// "" and no error when the package holds no types for subpath, so the lookup can go on
// to another package directory.
func packageTypesFile(pkgDir, subpath string) (string, error) {
	manifest, err := readManifest(pkgDir)
	if err != nil {
		return "", err
	}
	if manifest.exports != nil {
		key := "."
		if subpath != "" {
			key = "./" + subpath
		}
		value, star, listed := exportsEntry(manifest.exports, key)
		if !listed {
			return "", fmt.Errorf("%s does not export %q", filepath.Join(pkgDir, "package.json"), key)
		}
		path, missing := exportsTarget(pkgDir, value, star)
		if path != "" {
			return path, nil
		}
		if missing != "" {
			return "", fmt.Errorf("types entry point from exports not found: %s", missing)
		}
	}
	if subpath != "" {
		if path := typesVersionsFile(pkgDir, manifest, subpath); path != "" {
			return path, nil
		}
		return subpathFile(pkgDir, subpath)
	}
	return entryFile(pkgDir, manifest)
}

// entryFile returns the file the `types`, `typings` or `main` field of manifest names,
// or `index.d.ts`, as GetTypesEntryPoint orders them. A `typesVersions` redirect of
// the named file wins when it leads to a file that exists.
func entryFile(pkgDir string, manifest packageManifest) (string, error) {
	type field struct {
		rel, source string
	}
	var chosen field
	switch {
	case !manifest.found:
		chosen = field{rel: "index.d.ts"}
	case manifest.types != "":
		chosen = field{rel: manifest.types, source: " (from 'types' field)"}
	case manifest.typings != "":
		chosen = field{rel: manifest.typings, source: " (from 'typings' field)"}
	case manifest.main != "":
		main := manifest.main
		for _, ext := range []string{".mjs", ".cjs", ".js"} {
			if cut, found := strings.CutSuffix(main, ext); found {
				main = cut
				break
			}
		}
		chosen = field{rel: main + ".d.ts", source: " (derived from 'main' field)"}
	default:
		chosen = field{rel: "index.d.ts", source: " (fallback)"}
	}
	rel := strings.TrimPrefix(filepath.ToSlash(chosen.rel), "./")
	if path := typesVersionsFile(pkgDir, manifest, rel); path != "" {
		return path, nil
	}
	path := joinRel(pkgDir, rel)
	if !isFile(path) {
		return "", fmt.Errorf("types entry point not found: %s%s", path, chosen.source)
	}
	return path, nil
}

// typesVersionsFile returns the first file a `typesVersions` redirect of rel leads to
// inside pkgDir, or "" when the field redirects rel nowhere that exists.
func typesVersionsFile(pkgDir string, manifest packageManifest, rel string) string {
	for _, redirected := range typesVersionsPaths(manifest.typesVersions, rel) {
		if path := firstDeclarationFile(pkgDir, strings.TrimPrefix(redirected, "./")); path != "" {
			return path
		}
	}
	return ""
}

// subpathFile returns the file a subpath names inside the package in pkgDir, found on
// disk. A directory of that name is read as a package of its own, and a `.d.ts` file
// of that name stands for it. It returns "" and no error when neither exists.
func subpathFile(pkgDir, subpath string) (string, error) {
	target := joinRel(pkgDir, subpath)
	if isDir(target) {
		path, err := GetTypesEntryPoint(target)
		if err == nil {
			return path, nil
		}
		// `react/jsx-runtime` can name a file beside a directory of the same name.
		if file := target + ".d.ts"; isFile(file) {
			return file, nil
		}
		return "", err
	}
	if file := target + ".d.ts"; isFile(file) {
		return file, nil
	}
	return "", nil
}

// joinRel joins rel, a slash-separated path inside a package, onto pkgDir.
func joinRel(pkgDir, rel string) string {
	return filepath.Join(pkgDir, filepath.FromSlash(rel))
}
