package tests

import (
	"testing"

	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGlobalScopeInitialization verifies that the global scope is properly
// initialized when Prelude() is called.
func TestGlobalScopeInitialization(t *testing.T) {
	c := NewChecker()

	// Before calling Prelude, GlobalScope should be nil
	assert.Nil(t, c.GlobalScope, "GlobalScope should be nil before Prelude is called")

	// Call Prelude to initialize the global scope
	userScope := Prelude(c)

	// After calling Prelude, GlobalScope should be set
	require.NotNil(t, c.GlobalScope, "GlobalScope should be set after Prelude is called")

	// The global scope should have no parent (it's the root of the scope chain)
	assert.Nil(t, c.GlobalScope.Parent, "GlobalScope should have no parent")

	// The user scope returned by Prelude should have GlobalScope as its parent
	assert.Equal(t, c.GlobalScope, userScope.Parent, "User scope should have GlobalScope as parent")
}

// TestGlobalScopeContainsBuiltins verifies that the global scope contains
// TypeScript built-in types like Array, String, Number, etc.
func TestGlobalScopeContainsBuiltins(t *testing.T) {
	c := NewChecker()
	_ = Prelude(c)

	require.NotNil(t, c.GlobalScope, "GlobalScope should be set")

	globalNs := c.GlobalScope.Namespace
	require.NotNil(t, globalNs, "GlobalScope namespace should not be nil")

	// Check that common built-in types exist
	builtinTypes := []string{"Array", "String", "Number", "Boolean", "Object", "Promise"}
	for _, typeName := range builtinTypes {
		typeAlias, exists := globalNs.Types[typeName]
		assert.True(t, exists, "Global scope should contain type %s", typeName)
		if exists {
			assert.NotNil(t, typeAlias, "Type alias for %s should not be nil", typeName)
		}
	}

	// Check that built-in operators exist
	operators := []string{"+", "-", "*", "/", "==", "!=", "<", ">", "<=", ">=", "&&", "||", "!"}
	for _, op := range operators {
		binding, exists := globalNs.Values[op]
		assert.True(t, exists, "Global scope should contain operator %s", op)
		if exists {
			assert.NotNil(t, binding, "Binding for operator %s should not be nil", op)
		}
	}

	// Check that Symbol exists
	symbolBinding, exists := globalNs.Values["Symbol"]
	assert.True(t, exists, "Global scope should contain Symbol")
	if exists {
		assert.NotNil(t, symbolBinding, "Symbol binding should not be nil")
	}
}

// TestGlobalScopeReuse verifies that calling Prelude multiple times reuses
// the cached global scope.
func TestGlobalScopeReuse(t *testing.T) {
	c1 := NewChecker()
	userScope1 := Prelude(c1)
	globalScope1 := c1.GlobalScope

	c2 := NewChecker()
	userScope2 := Prelude(c2)
	globalScope2 := c2.GlobalScope

	// Both checkers should share the same global scope (cached)
	assert.Same(t, globalScope1, globalScope2, "Cached global scope should be the same pointer")

	// User scopes should be different pointers (fresh child scopes)
	assert.NotSame(t, userScope1, userScope2, "User scopes should be distinct pointers")

	// Both user scopes should have the same parent (the cached global scope)
	assert.Same(t, globalScope1, userScope1.Parent, "User scope 1 parent should be global scope")
	assert.Same(t, globalScope2, userScope2.Parent, "User scope 2 parent should be global scope")
}

// TestGlobalScopeLookupChain verifies that lookups traverse the scope chain
// correctly from user scope to global scope.
func TestGlobalScopeLookupChain(t *testing.T) {
	c := NewChecker()
	userScope := Prelude(c)

	require.NotNil(t, c.GlobalScope, "GlobalScope should be set")

	// Look up a built-in type from the user scope
	// This should traverse to the global scope
	arrayType := userScope.GetValue("Array")
	assert.NotNil(t, arrayType, "Should find Array in global scope via parent chain lookup")

	// Look up an operator from the user scope
	plusOp := userScope.GetValue("+")
	assert.NotNil(t, plusOp, "Should find + operator in global scope via parent chain lookup")

	// Verify the global scope namespace contains these directly
	globalArrayType := c.GlobalScope.Namespace.Values["Array"]
	assert.NotNil(t, globalArrayType, "Array should be directly in global namespace")
	assert.Equal(t, globalArrayType, arrayType, "Lookup result should match direct access")
}

// TestUserScopeIsolatedFromGlobalScope verifies that adding bindings to the
// user scope doesn't affect the global scope.
func TestUserScopeIsolatedFromGlobalScope(t *testing.T) {
	c := NewChecker()
	userScope := Prelude(c)

	// Count bindings in global scope before adding to user scope
	globalTypeCount := len(c.GlobalScope.Namespace.Types)
	globalValueCount := len(c.GlobalScope.Namespace.Values)

	// Add a type and value to the user scope
	userScope.Namespace.Types["MyCustomType"] = nil
	userScope.Namespace.Values["myCustomValue"] = nil

	// Global scope should be unchanged
	assert.Equal(t, globalTypeCount, len(c.GlobalScope.Namespace.Types),
		"Adding to user scope should not affect global scope types")
	assert.Equal(t, globalValueCount, len(c.GlobalScope.Namespace.Values),
		"Adding to user scope should not affect global scope values")

	// User scope should have the new bindings
	_, hasCustomType := userScope.Namespace.Types["MyCustomType"]
	_, hasCustomValue := userScope.Namespace.Values["myCustomValue"]
	assert.True(t, hasCustomType, "User scope should have the custom type")
	assert.True(t, hasCustomValue, "User scope should have the custom value")

	// Global scope should not have the new bindings
	_, globalHasCustomType := c.GlobalScope.Namespace.Types["MyCustomType"]
	_, globalHasCustomValue := c.GlobalScope.Namespace.Values["myCustomValue"]
	assert.False(t, globalHasCustomType, "Global scope should not have the custom type")
	assert.False(t, globalHasCustomValue, "Global scope should not have the custom value")
}

// TestPackageRegistryInitialized verifies that the PackageRegistry is available
// and can store named modules.
func TestPackageRegistryInitialized(t *testing.T) {
	c := NewChecker()

	// PackageRegistry should be initialized by NewChecker
	require.NotNil(t, c.PackageRegistry, "PackageRegistry should be initialized")

	// After Prelude, it should still be available
	_ = Prelude(c)
	require.NotNil(t, c.PackageRegistry, "PackageRegistry should still be available after Prelude")
}
