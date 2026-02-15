package checker

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/type_system"
)

// PackageRegistry stores package namespaces separate from the scope chain.
// Packages are registered by their resolved .d.ts file path (not the package name).
// This design supports monorepos where different Escalier packages may depend on
// different versions of the same npm package - each version will have a unique
// file path and thus a separate registry entry.
//
// Example keys:
//   - "/path/to/project-a/node_modules/lodash/index.d.ts" (lodash v4.17.21)
//   - "/path/to/project-b/node_modules/lodash/index.d.ts" (lodash v4.17.15)
//
// Use resolveImport() to resolve a package name (e.g., "lodash") to its .d.ts
// file path for registry lookup.
type PackageRegistry struct {
	packages map[string]*type_system.Namespace
}

// NewPackageRegistry creates a new empty package registry.
func NewPackageRegistry() *PackageRegistry {
	return &PackageRegistry{
		packages: make(map[string]*type_system.Namespace),
	}
}

// Register adds a package namespace to the registry using the resolved .d.ts file path as the key.
// If a package with the same file path already exists, it returns an error.
func (pr *PackageRegistry) Register(dtsFilePath string, ns *type_system.Namespace) error {
	if dtsFilePath == "" {
		return fmt.Errorf("package file path cannot be empty")
	}
	if ns == nil {
		return fmt.Errorf("package namespace cannot be nil")
	}
	if _, exists := pr.packages[dtsFilePath]; exists {
		return fmt.Errorf("package at %q is already registered", dtsFilePath)
	}
	pr.packages[dtsFilePath] = ns
	return nil
}

// Lookup returns the namespace for a package by its resolved .d.ts file path.
// Returns (namespace, true) if found, or (nil, false) if not found.
func (pr *PackageRegistry) Lookup(dtsFilePath string) (*type_system.Namespace, bool) {
	ns, ok := pr.packages[dtsFilePath]
	return ns, ok
}

// MustLookup returns the namespace for a package by its resolved .d.ts file path.
// Panics if the package is not found. Use this only for internal lookups
// where the package is guaranteed to exist.
func (pr *PackageRegistry) MustLookup(dtsFilePath string) *type_system.Namespace {
	ns, ok := pr.packages[dtsFilePath]
	if !ok {
		panic(fmt.Sprintf("package at %q not found in registry", dtsFilePath))
	}
	return ns
}

// Has returns true if a package with the given file path is registered.
func (pr *PackageRegistry) Has(dtsFilePath string) bool {
	_, ok := pr.packages[dtsFilePath]
	return ok
}
