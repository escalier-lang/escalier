package checker

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/type_system"
)

// PackageRegistry stores package namespaces separate from the scope chain.
// Packages are registered by their identity (e.g., "lodash", "@types/node")
// and can be looked up when processing import statements.
type PackageRegistry struct {
	packages map[string]*type_system.Namespace
}

// NewPackageRegistry creates a new empty package registry.
func NewPackageRegistry() *PackageRegistry {
	return &PackageRegistry{
		packages: make(map[string]*type_system.Namespace),
	}
}

// Register adds a package namespace to the registry.
// If a package with the same identity already exists, it returns an error.
func (pr *PackageRegistry) Register(identity string, ns *type_system.Namespace) error {
	if identity == "" {
		return fmt.Errorf("package identity cannot be empty")
	}
	if ns == nil {
		return fmt.Errorf("package namespace cannot be nil")
	}
	if _, exists := pr.packages[identity]; exists {
		return fmt.Errorf("package %q is already registered", identity)
	}
	pr.packages[identity] = ns
	return nil
}

// Lookup returns the namespace for a package identity.
// Returns (namespace, true) if found, or (nil, false) if not found.
func (pr *PackageRegistry) Lookup(identity string) (*type_system.Namespace, bool) {
	ns, ok := pr.packages[identity]
	return ns, ok
}

// MustLookup returns the namespace for a package identity.
// Panics if the package is not found. Use this only for internal lookups
// where the package is guaranteed to exist.
func (pr *PackageRegistry) MustLookup(identity string) *type_system.Namespace {
	ns, ok := pr.packages[identity]
	if !ok {
		panic(fmt.Sprintf("package %q not found in registry", identity))
	}
	return ns
}

// Has returns true if a package with the given identity is registered.
func (pr *PackageRegistry) Has(identity string) bool {
	_, ok := pr.packages[identity]
	return ok
}
