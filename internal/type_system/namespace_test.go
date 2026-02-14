package type_system

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNamespaceSetNamespace(t *testing.T) {
	ns := NewNamespace()
	subNs := NewNamespace()
	subNs.Values["value"] = &Binding{Type: NewNumPrimType(nil), Mutable: false}

	// Set a sub-namespace
	err := ns.SetNamespace("lodash", subNs)
	assert.NoError(t, err, "Should set sub-namespace without error")

	// Verify it was set
	found, ok := ns.GetNamespace("lodash")
	assert.True(t, ok, "Should find the sub-namespace")
	assert.Equal(t, subNs, found, "Should return the same sub-namespace")
}

func TestNamespaceGetNamespaceNotFound(t *testing.T) {
	ns := NewNamespace()

	// Get a non-existent sub-namespace
	found, ok := ns.GetNamespace("non-existent")
	assert.False(t, ok, "Should not find non-existent sub-namespace")
	assert.Nil(t, found, "Should return nil for non-existent sub-namespace")
}

func TestNamespaceSetNamespaceConflictWithType(t *testing.T) {
	ns := NewNamespace()
	subNs := NewNamespace()

	// Add a type with the same name
	ns.Types["lodash"] = &TypeAlias{Type: NewNumPrimType(nil)}

	// Try to set a sub-namespace with the same name
	err := ns.SetNamespace("lodash", subNs)
	assert.Error(t, err, "Should fail when conflicting with existing type")
	assert.Contains(t, err.Error(), "conflicts with existing type")
}

func TestNamespaceSetNamespaceConflictWithValue(t *testing.T) {
	ns := NewNamespace()
	subNs := NewNamespace()

	// Add a value with the same name
	ns.Values["lodash"] = &Binding{Type: NewNumPrimType(nil), Mutable: false}

	// Try to set a sub-namespace with the same name
	err := ns.SetNamespace("lodash", subNs)
	assert.Error(t, err, "Should fail when conflicting with existing value")
	assert.Contains(t, err.Error(), "conflicts with existing value")
}

func TestNamespaceSetNamespaceNilNamespacesMap(t *testing.T) {
	// Create a namespace with nil Namespaces map (edge case)
	ns := &Namespace{
		Values:     make(map[string]*Binding),
		Types:      make(map[string]*TypeAlias),
		Namespaces: nil, // Intentionally nil
	}
	subNs := NewNamespace()

	// SetNamespace should initialize the map if nil
	err := ns.SetNamespace("lodash", subNs)
	assert.NoError(t, err, "Should handle nil Namespaces map")

	// Verify it was set
	found, ok := ns.GetNamespace("lodash")
	assert.True(t, ok, "Should find the sub-namespace")
	assert.Equal(t, subNs, found)
}

func TestNamespaceGetNamespaceNilNamespacesMap(t *testing.T) {
	// Create a namespace with nil Namespaces map (edge case)
	ns := &Namespace{
		Values:     make(map[string]*Binding),
		Types:      make(map[string]*TypeAlias),
		Namespaces: nil, // Intentionally nil
	}

	// GetNamespace should handle nil map gracefully
	found, ok := ns.GetNamespace("lodash")
	assert.False(t, ok, "Should not find namespace in nil map")
	assert.Nil(t, found)
}

func TestNamespaceMultipleSubNamespaces(t *testing.T) {
	ns := NewNamespace()

	subNs1 := NewNamespace()
	subNs1.Values["map"] = &Binding{Type: NewNumPrimType(nil), Mutable: false}

	subNs2 := NewNamespace()
	subNs2.Values["map"] = &Binding{Type: NewStrPrimType(nil), Mutable: false}

	subNs3 := NewNamespace()
	subNs3.Values["readFile"] = &Binding{Type: NewBoolPrimType(nil), Mutable: false}

	// Set multiple sub-namespaces
	assert.NoError(t, ns.SetNamespace("lodash", subNs1))
	assert.NoError(t, ns.SetNamespace("ramda", subNs2))
	assert.NoError(t, ns.SetNamespace("fs", subNs3))

	// Verify each can be retrieved independently
	found1, ok1 := ns.GetNamespace("lodash")
	assert.True(t, ok1)
	assert.Equal(t, subNs1, found1)

	found2, ok2 := ns.GetNamespace("ramda")
	assert.True(t, ok2)
	assert.Equal(t, subNs2, found2)

	found3, ok3 := ns.GetNamespace("fs")
	assert.True(t, ok3)
	assert.Equal(t, subNs3, found3)
}

func TestNamespaceNestedSubNamespaces(t *testing.T) {
	// Create a hierarchy: root -> lodash -> fp
	root := NewNamespace()
	lodash := NewNamespace()
	fp := NewNamespace()

	fp.Values["map"] = &Binding{Type: NewNumPrimType(nil), Mutable: false}

	// Set up the hierarchy
	assert.NoError(t, lodash.SetNamespace("fp", fp))
	assert.NoError(t, root.SetNamespace("lodash", lodash))

	// Navigate the hierarchy
	foundLodash, ok1 := root.GetNamespace("lodash")
	assert.True(t, ok1)

	foundFp, ok2 := foundLodash.GetNamespace("fp")
	assert.True(t, ok2)

	// Verify nested namespace contents
	binding, exists := foundFp.Values["map"]
	assert.True(t, exists)
	assert.NotNil(t, binding)
}
