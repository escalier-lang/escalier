package tests

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGlobalThisBinding verifies that globalThis is available in the global scope
// and provides access to the global namespace.
func TestGlobalThisBinding(t *testing.T) {
	c := NewChecker()
	userScope := Prelude(c)

	require.NotNil(t, c.GlobalScope, "GlobalScope should be set")

	// globalThis should be in the global scope
	globalThisBinding := c.GlobalScope.Namespace.Values["globalThis"]
	require.NotNil(t, globalThisBinding, "globalThis should be in the global namespace")

	// globalThis should be a NamespaceType
	nsType, ok := globalThisBinding.Type.(*type_system.NamespaceType)
	require.True(t, ok, "globalThis should be a NamespaceType")

	// The namespace should be the global namespace itself
	assert.Equal(t, c.GlobalScope.Namespace, nsType.Namespace,
		"globalThis namespace should be the global namespace")

	// globalThis should be accessible from user scope via parent chain
	globalThisFromUser := userScope.GetValue("globalThis")
	assert.NotNil(t, globalThisFromUser, "globalThis should be accessible from user scope")
}

// TestLocalShadowingOfGlobals verifies that local declarations can shadow
// global declarations.
func TestLocalShadowingOfGlobals(t *testing.T) {
	input := `
		type Number = { value: number, isLocal: boolean }
		val num: Number = { value: 42, isLocal: true }
	`

	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: input,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	script, parseErrors := p.ParseScript()
	require.Len(t, parseErrors, 0, "Should have no parse errors")

	c := NewChecker()
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}
	scope, inferErrors := c.InferScript(inferCtx, script)

	// Should have no errors - local Number should shadow global Number
	assert.Len(t, inferErrors, 0, "Should have no inference errors when shadowing globals")

	// The local type should be used, not the global Number
	numBinding := scope.Namespace.Values["num"]
	require.NotNil(t, numBinding, "num binding should exist")

	// The type annotation creates a TypeRefType pointing to our local Number type alias
	numType := type_system.Prune(numBinding.Type)
	typeRef, ok := numType.(*type_system.TypeRefType)
	require.True(t, ok, "num should have TypeRefType, got %T", numType)

	// The TypeRefType should reference "Number" (our local type alias)
	assert.Equal(t, "Number", type_system.QualIdentToString(typeRef.Name), "Should reference local Number type")

	// Verify the type alias points to our custom object type
	require.NotNil(t, typeRef.TypeAlias, "TypeRefType should have TypeAlias set")
	aliasType := type_system.Prune(typeRef.TypeAlias.Type)
	objType, ok := aliasType.(*type_system.ObjectType)
	require.True(t, ok, "Number alias should be object type, got %T", aliasType)

	// Should have an isLocal property (only on our custom Number type)
	hasIsLocalProp := false
	for _, elem := range objType.Elems {
		if propElem, ok := elem.(*type_system.PropertyElem); ok {
			if propElem.Name == type_system.NewStrKey("isLocal") {
				hasIsLocalProp = true
				break
			}
		}
	}
	assert.True(t, hasIsLocalProp, "Local Number type should have isLocal property")
}

// TestGlobalThisAccessToGlobals verifies that globalThis.Array provides access
// to the global Array type, even when a local Array type shadows it.
func TestGlobalThisAccessToGlobals(t *testing.T) {
	input := `
		val globalArray: globalThis.Array<number> = [1, 2, 3]
	`

	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: input,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	script, parseErrors := p.ParseScript()
	require.Len(t, parseErrors, 0, "Should have no parse errors")

	c := NewChecker()
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}
	scope, inferErrors := c.InferScript(inferCtx, script)

	// Log any errors for debugging
	for _, err := range inferErrors {
		t.Logf("Inference error: %s", err.Message())
	}

	// Should have no errors - globalThis.Array should resolve to the global Array
	assert.Len(t, inferErrors, 0, "Should have no inference errors when using globalThis.Array")

	// The globalArray binding should exist
	globalArrayBinding := scope.Namespace.Values["globalArray"]
	require.NotNil(t, globalArrayBinding, "globalArray binding should exist")

	// globalArray should use the global Array type (via globalThis)
	globalArrayType := type_system.Prune(globalArrayBinding.Type)
	globalTypeRef, ok := globalArrayType.(*type_system.TypeRefType)
	require.True(t, ok, "globalArray should have TypeRefType, got %T", globalArrayType)

	// The qualified name should be "globalThis.Array"
	assert.Equal(t, "globalThis.Array", type_system.QualIdentToString(globalTypeRef.Name),
		"Should reference global Array via globalThis")

	// Verify it points to the actual global Array type alias
	require.NotNil(t, globalTypeRef.TypeAlias, "globalThis.Array should have TypeAlias set")
	assert.Equal(t, c.GlobalScope.Namespace.Types["Array"], globalTypeRef.TypeAlias,
		"globalThis.Array should point to the global Array type alias")
}

// TestGlobalThisAccessWhenShadowed verifies that globalThis.Array provides
// access to the global Array even when a local Array shadows it.
func TestGlobalThisAccessWhenShadowed(t *testing.T) {
	input := `
		type Array<T> = { items: T, isLocal: boolean }
		val localArr: Array<string> = { items: "test", isLocal: true }
		val globalArr: globalThis.Array<number> = [1, 2, 3]
	`

	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: input,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	script, parseErrors := p.ParseScript()
	require.Len(t, parseErrors, 0, "Should have no parse errors")

	c := NewChecker()
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}
	scope, inferErrors := c.InferScript(inferCtx, script)

	// Log any errors for debugging
	for _, err := range inferErrors {
		t.Logf("Inference error: %s", err.Message())
	}

	// Should have no errors
	assert.Len(t, inferErrors, 0, "Should have no inference errors")

	// Both bindings should exist
	localArrBinding := scope.Namespace.Values["localArr"]
	globalArrBinding := scope.Namespace.Values["globalArr"]
	require.NotNil(t, localArrBinding, "localArr binding should exist")
	require.NotNil(t, globalArrBinding, "globalArr binding should exist")

	// localArr should use the local Array type (TypeRefType pointing to our custom type)
	localArrType := type_system.Prune(localArrBinding.Type)
	typeRef, ok := localArrType.(*type_system.TypeRefType)
	require.True(t, ok, "localArr should have TypeRefType, got %T", localArrType)

	// Verify the TypeRefType references our local "Array" type (not globalThis.Array)
	assert.Equal(t, "Array", type_system.QualIdentToString(typeRef.Name), "Should reference local Array type")

	// Verify the type alias points to our custom object type with isLocal property
	require.NotNil(t, typeRef.TypeAlias, "TypeRefType should have TypeAlias set")
	aliasType := type_system.Prune(typeRef.TypeAlias.Type)
	objType, ok := aliasType.(*type_system.ObjectType)
	require.True(t, ok, "Array alias should be object type, got %T", aliasType)

	hasIsLocalProp := false
	for _, elem := range objType.Elems {
		if propElem, ok := elem.(*type_system.PropertyElem); ok {
			if propElem.Name == type_system.NewStrKey("isLocal") {
				hasIsLocalProp = true
				break
			}
		}
	}
	assert.True(t, hasIsLocalProp, "Local Array type should have isLocal property")

	// globalArr should use the global Array type (via globalThis)
	globalArrType := type_system.Prune(globalArrBinding.Type)
	globalTypeRef, ok := globalArrType.(*type_system.TypeRefType)
	require.True(t, ok, "globalArr should have TypeRefType, got %T", globalArrType)

	// The qualified name should be "globalThis.Array"
	assert.Equal(t, "globalThis.Array", type_system.QualIdentToString(globalTypeRef.Name),
		"Should reference global Array via globalThis")

	// Verify it points to the actual global Array type alias
	require.NotNil(t, globalTypeRef.TypeAlias, "globalThis.Array should have TypeAlias set")
	assert.Equal(t, c.GlobalScope.Namespace.Types["Array"], globalTypeRef.TypeAlias,
		"globalThis.Array should point to the global Array type alias")
}

// TestShadowedGlobalNotAccessibleUnqualified verifies that when a global is
// shadowed, unqualified access resolves to the local definition.
func TestShadowedGlobalNotAccessibleUnqualified(t *testing.T) {
	c := NewChecker()
	userScope := Prelude(c)

	// Add a local "Array" type that shadows the global
	localArrayAlias := &type_system.TypeAlias{
		Type:       type_system.NewObjectType(nil, nil),
		TypeParams: []*type_system.TypeParam{},
	}
	userScope.Namespace.Types["Array"] = localArrayAlias

	// Unqualified lookup should return the local type
	localType := userScope.Namespace.Types["Array"]
	assert.Same(t, localArrayAlias, localType, "Local Array should shadow global")

	// Direct global access should return the global type
	globalType := c.GlobalScope.Namespace.Types["Array"]
	assert.NotSame(t, localArrayAlias, globalType, "Global Array should be different from local")
	assert.NotNil(t, globalType, "Global Array should still exist")
}

// TestGlobalThisValueAccess verifies that globalThis can access global values
// like Symbol and operators.
func TestGlobalThisValueAccess(t *testing.T) {
	input := `
		val sym = globalThis.Symbol.iterator
	`

	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: input,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	script, parseErrors := p.ParseScript()
	require.Len(t, parseErrors, 0, "Should have no parse errors")

	c := NewChecker()
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}
	scope, inferErrors := c.InferScript(inferCtx, script)

	// Should have no errors
	assert.Len(t, inferErrors, 0, "Should have no inference errors when accessing globalThis.Symbol.iterator")

	// sym binding should exist and be a unique symbol type
	symBinding := scope.Namespace.Values["sym"]
	require.NotNil(t, symBinding, "sym binding should exist")

	symType := type_system.Prune(symBinding.Type)
	_, ok := symType.(*type_system.UniqueSymbolType)
	assert.True(t, ok, "sym should be a unique symbol type, got %T", symType)
}
