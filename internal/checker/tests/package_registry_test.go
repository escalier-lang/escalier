package tests

import (
	"testing"

	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
)

func TestPackageRegistryRegisterAndLookup(t *testing.T) {
	registry := NewPackageRegistry()

	// Create a test namespace
	ns := type_system.NewNamespace()
	numType := type_system.NewNumPrimType(nil)
	ns.Values["testValue"] = &type_system.Binding{
		Type:    numType,
		Mutable: false,
	}

	// Register the package using file path as key
	filePath := "/path/to/node_modules/test-package/index.d.ts"
	err := registry.Register(filePath, ns)
	assert.NoError(t, err, "Should register package without error")

	// Lookup the package by file path
	foundNs, found := registry.Lookup(filePath)
	assert.True(t, found, "Should find registered package")
	assert.Equal(t, ns, foundNs, "Should return the same namespace")

	// Verify the namespace contents
	binding, exists := foundNs.Values["testValue"]
	assert.True(t, exists, "Should contain testValue")
	assert.Equal(t, numType, binding.Type)
}

func TestPackageRegistryLookupNotFound(t *testing.T) {
	registry := NewPackageRegistry()

	// Lookup a non-existent package
	ns, found := registry.Lookup("/path/to/non-existent/index.d.ts")
	assert.False(t, found, "Should not find non-existent package")
	assert.Nil(t, ns, "Should return nil for non-existent package")
}

func TestPackageRegistryDuplicateRegistration(t *testing.T) {
	registry := NewPackageRegistry()
	ns := type_system.NewNamespace()

	filePath := "/path/to/node_modules/my-package/index.d.ts"

	// First registration should succeed
	err1 := registry.Register(filePath, ns)
	assert.NoError(t, err1, "First registration should succeed")

	// Second registration with same file path should fail
	err2 := registry.Register(filePath, ns)
	assert.Error(t, err2, "Duplicate registration should fail")
	assert.Contains(t, err2.Error(), "already registered")
}

func TestPackageRegistryEmptyFilePath(t *testing.T) {
	registry := NewPackageRegistry()
	ns := type_system.NewNamespace()

	err := registry.Register("", ns)
	assert.Error(t, err, "Empty file path should fail")
	assert.Contains(t, err.Error(), "cannot be empty")
}

func TestPackageRegistryNilNamespace(t *testing.T) {
	registry := NewPackageRegistry()

	err := registry.Register("/path/to/my-package/index.d.ts", nil)
	assert.Error(t, err, "Nil namespace should fail")
	assert.Contains(t, err.Error(), "cannot be nil")
}

func TestPackageRegistryHas(t *testing.T) {
	registry := NewPackageRegistry()
	ns := type_system.NewNamespace()

	filePath := "/path/to/node_modules/my-package/index.d.ts"

	// Before registration
	assert.False(t, registry.Has(filePath), "Should not have unregistered package")

	// After registration
	err := registry.Register(filePath, ns)
	assert.NoError(t, err)
	assert.True(t, registry.Has(filePath), "Should have registered package")
}

func TestPackageRegistryMustLookup(t *testing.T) {
	registry := NewPackageRegistry()
	ns := type_system.NewNamespace()

	filePath := "/path/to/node_modules/my-package/index.d.ts"
	err := registry.Register(filePath, ns)
	assert.NoError(t, err)

	// MustLookup should return the namespace
	foundNs := registry.MustLookup(filePath)
	assert.Equal(t, ns, foundNs)
}

func TestPackageRegistryMustLookupPanics(t *testing.T) {
	registry := NewPackageRegistry()

	// MustLookup should panic for non-existent package
	assert.Panics(t, func() {
		registry.MustLookup("/path/to/non-existent/index.d.ts")
	}, "MustLookup should panic for non-existent package")
}

func TestPackageRegistryMultiplePackages(t *testing.T) {
	registry := NewPackageRegistry()

	// Create multiple namespaces
	ns1 := type_system.NewNamespace()
	ns1.Values["value1"] = &type_system.Binding{Type: type_system.NewNumPrimType(nil), Mutable: false}

	ns2 := type_system.NewNamespace()
	ns2.Values["value2"] = &type_system.Binding{Type: type_system.NewStrPrimType(nil), Mutable: false}

	ns3 := type_system.NewNamespace()
	ns3.Values["value3"] = &type_system.Binding{Type: type_system.NewBoolPrimType(nil), Mutable: false}

	// Register all packages using file paths as keys
	lodashPath := "/path/to/node_modules/lodash/index.d.ts"
	ramdaPath := "/path/to/node_modules/ramda/index.d.ts"
	nodePath := "/path/to/node_modules/@types/node/index.d.ts"

	assert.NoError(t, registry.Register(lodashPath, ns1))
	assert.NoError(t, registry.Register(ramdaPath, ns2))
	assert.NoError(t, registry.Register(nodePath, ns3))

	// Lookup each package by file path
	found1, ok1 := registry.Lookup(lodashPath)
	assert.True(t, ok1)
	assert.Equal(t, ns1, found1)

	found2, ok2 := registry.Lookup(ramdaPath)
	assert.True(t, ok2)
	assert.Equal(t, ns2, found2)

	found3, ok3 := registry.Lookup(nodePath)
	assert.True(t, ok3)
	assert.Equal(t, ns3, found3)
}

// TestPackageRegistryMonorepoSupport verifies that different versions of the same
// package can be registered when they come from different file paths (monorepo support)
func TestPackageRegistryMonorepoSupport(t *testing.T) {
	registry := NewPackageRegistry()

	// Two different versions of lodash in different project directories
	ns1 := type_system.NewNamespace()
	ns1.Values["version"] = &type_system.Binding{
		Type:    type_system.NewStrLitType(nil, "4.17.21"),
		Mutable: false,
	}

	ns2 := type_system.NewNamespace()
	ns2.Values["version"] = &type_system.Binding{
		Type:    type_system.NewStrLitType(nil, "4.17.15"),
		Mutable: false,
	}

	// These are different file paths even though they're the "same" package
	projectALodash := "/monorepo/packages/project-a/node_modules/lodash/index.d.ts"
	projectBLodash := "/monorepo/packages/project-b/node_modules/lodash/index.d.ts"

	// Both should register successfully since they have different file paths
	assert.NoError(t, registry.Register(projectALodash, ns1))
	assert.NoError(t, registry.Register(projectBLodash, ns2))

	// Each should be retrievable independently
	found1, ok1 := registry.Lookup(projectALodash)
	assert.True(t, ok1)
	assert.Equal(t, ns1, found1)

	found2, ok2 := registry.Lookup(projectBLodash)
	assert.True(t, ok2)
	assert.Equal(t, ns2, found2)

	// Verify they are different namespaces
	assert.NotEqual(t, found1, found2, "Should be different namespaces for different versions")
}
