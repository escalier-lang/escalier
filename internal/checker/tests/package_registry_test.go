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

	// Register the package
	err := registry.Register("test-package", ns)
	assert.NoError(t, err, "Should register package without error")

	// Lookup the package
	foundNs, found := registry.Lookup("test-package")
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
	ns, found := registry.Lookup("non-existent")
	assert.False(t, found, "Should not find non-existent package")
	assert.Nil(t, ns, "Should return nil for non-existent package")
}

func TestPackageRegistryDuplicateRegistration(t *testing.T) {
	registry := NewPackageRegistry()
	ns := type_system.NewNamespace()

	// First registration should succeed
	err1 := registry.Register("my-package", ns)
	assert.NoError(t, err1, "First registration should succeed")

	// Second registration with same identity should fail
	err2 := registry.Register("my-package", ns)
	assert.Error(t, err2, "Duplicate registration should fail")
	assert.Contains(t, err2.Error(), "already registered")
}

func TestPackageRegistryEmptyIdentity(t *testing.T) {
	registry := NewPackageRegistry()
	ns := type_system.NewNamespace()

	err := registry.Register("", ns)
	assert.Error(t, err, "Empty identity should fail")
	assert.Contains(t, err.Error(), "cannot be empty")
}

func TestPackageRegistryNilNamespace(t *testing.T) {
	registry := NewPackageRegistry()

	err := registry.Register("my-package", nil)
	assert.Error(t, err, "Nil namespace should fail")
	assert.Contains(t, err.Error(), "cannot be nil")
}

func TestPackageRegistryHas(t *testing.T) {
	registry := NewPackageRegistry()
	ns := type_system.NewNamespace()

	// Before registration
	assert.False(t, registry.Has("my-package"), "Should not have unregistered package")

	// After registration
	err := registry.Register("my-package", ns)
	assert.NoError(t, err)
	assert.True(t, registry.Has("my-package"), "Should have registered package")
}

func TestPackageRegistryMustLookup(t *testing.T) {
	registry := NewPackageRegistry()
	ns := type_system.NewNamespace()

	err := registry.Register("my-package", ns)
	assert.NoError(t, err)

	// MustLookup should return the namespace
	foundNs := registry.MustLookup("my-package")
	assert.Equal(t, ns, foundNs)
}

func TestPackageRegistryMustLookupPanics(t *testing.T) {
	registry := NewPackageRegistry()

	// MustLookup should panic for non-existent package
	assert.Panics(t, func() {
		registry.MustLookup("non-existent")
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

	// Register all packages
	assert.NoError(t, registry.Register("lodash", ns1))
	assert.NoError(t, registry.Register("ramda", ns2))
	assert.NoError(t, registry.Register("@types/node", ns3))

	// Lookup each package
	found1, ok1 := registry.Lookup("lodash")
	assert.True(t, ok1)
	assert.Equal(t, ns1, found1)

	found2, ok2 := registry.Lookup("ramda")
	assert.True(t, ok2)
	assert.Equal(t, ns2, found2)

	found3, ok3 := registry.Lookup("@types/node")
	assert.True(t, ok3)
	assert.Equal(t, ns3, found3)
}
