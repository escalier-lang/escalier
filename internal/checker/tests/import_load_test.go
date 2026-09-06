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

// TestPackageLoadAndRegister verifies that loading a package registers it in the PackageRegistry
// NOTE: This test assumes "fast-deep-equal" is installed in node_modules and
// requires that `pnpm install` has been run in the project root before running
// this test.
func TestPackageLoadAndRegister(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create a script that imports a real package
	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: `import "fast-deep-equal" as fde`,
	}

	p := parser.NewParser(ctx, source)
	script, parseErrors := p.ParseScript()
	require.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker(ctx)
	inferCtx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	scriptScope, inferErrors := c.InferScript(inferCtx, script)
	// We expect this to work since fast-deep-equal is installed
	assert.Empty(t, inferErrors, "Should infer without errors")

	// Verify the fde namespace is bound in the script scope (not the parent)
	ns, found := scriptScope.Namespace.GetNamespace("fde")
	assert.True(t, found, "Should have 'fde' namespace bound")
	assert.NotNil(t, ns, "Namespace should not be nil")
}

// TestPackageReloadFromRegistry verifies that re-importing uses the cached namespace
func TestPackageReloadFromRegistry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Pre-register a mock package
	c := NewChecker(ctx)
	mockNs := type_system.NewNamespace()
	mockNs.Values["helper"] = &type_system.Binding{
		Type:     type_system.NewNumPrimType(nil),
		Mutable:  false,
		Exported: true,
	}

	// Register with package name (for backwards compatibility with test patterns)
	err := c.PackageRegistry.Register("test-utils", mockNs)
	require.NoError(t, err)

	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/file1.esc",
			Contents: `
				import "test-utils" as utils
			`,
		},
		{
			ID:   1,
			Path: "lib/file2.esc",
			Contents: `
				import "test-utils" as utils
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	require.Empty(t, parseErrors)

	inferCtx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	_, inferErrors := c.InferModule(inferCtx, module)
	assert.Empty(t, inferErrors, "Should infer without errors")
}

// TestSubpathImportSeparateEntries verifies that different subpaths are separate registry entries
func TestSubpathImportSeparateEntries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	c := NewChecker(ctx)

	// Pre-register mock packages for main and subpath with different types
	mainNs := type_system.NewNamespace()
	mainNs.Values["mainExport"] = &type_system.Binding{
		Type:     type_system.NewStrPrimType(nil),
		Mutable:  false,
		Exported: true,
	}

	fpNs := type_system.NewNamespace()
	fpNs.Values["fpExport"] = &type_system.Binding{
		Type:     type_system.NewNumPrimType(nil),
		Mutable:  false,
		Exported: true,
	}

	err := c.PackageRegistry.Register("lodash", mainNs)
	require.NoError(t, err)
	err = c.PackageRegistry.Register("lodash/fp", fpNs)
	require.NoError(t, err)

	// Verify imports work by using them in declarations
	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/main.esc",
			Contents: `
				import "lodash" as lodash
				import "lodash/fp" as fp
				val main = lodash.mainExport
				val fpVal = fp.fpExport
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	require.Empty(t, parseErrors)

	inferCtx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	_, inferErrors := c.InferModule(inferCtx, module)

	for _, err := range inferErrors {
		t.Logf("Error: %s", err.Message())
	}
	assert.Empty(t, inferErrors, "Subpath imports should work and be separate entries")
}

// TestNonExportedItemsAreFilteredFromNamespaceImport verifies that non-exported
// items in a package are not accessible via namespace imports (import * as alias).
func TestNonExportedItemsAreFilteredFromNamespaceImport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	c := NewChecker(ctx)

	// Create a package with both exported and non-exported items
	mockNs := type_system.NewNamespace()

	// Exported items - should be accessible
	mockNs.Values["publicFunc"] = &type_system.Binding{
		Type:     type_system.NewNumPrimType(nil),
		Mutable:  false,
		Exported: true,
	}
	mockNs.Types["PublicType"] = &type_system.TypeAlias{
		Type:       type_system.NewStrPrimType(nil),
		TypeParams: nil,
		Exported:   true,
	}

	// Non-exported items - should NOT be accessible
	mockNs.Values["internalHelper"] = &type_system.Binding{
		Type:     type_system.NewNumPrimType(nil),
		Mutable:  false,
		Exported: false,
	}
	mockNs.Types["InternalType"] = &type_system.TypeAlias{
		Type:       type_system.NewStrPrimType(nil),
		TypeParams: nil,
		Exported:   false,
	}

	err := c.PackageRegistry.Register("mixed-exports-pkg", mockNs)
	require.NoError(t, err)

	// Test that exported items are accessible
	sourcesGood := []*ast.Source{
		{
			ID:   0,
			Path: "lib/good.esc",
			Contents: `
				import "mixed-exports-pkg" as pkg
				val x = pkg.publicFunc
				declare val y: pkg.PublicType
			`,
		},
	}

	moduleGood, parseErrors := parser.ParseLibFiles(ctx, sourcesGood)
	require.Empty(t, parseErrors)

	inferCtx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	_, inferErrors := c.InferModule(inferCtx, moduleGood)
	assert.Empty(t, inferErrors, "Exported items should be accessible via namespace import")

	// Test that non-exported items cause errors when accessed
	c2 := NewChecker(ctx)
	err = c2.PackageRegistry.Register("mixed-exports-pkg", mockNs)
	require.NoError(t, err)

	sourcesBadValue := []*ast.Source{
		{
			ID:   0,
			Path: "lib/bad_value.esc",
			Contents: `
				import "mixed-exports-pkg" as pkg
				val x = pkg.internalHelper
			`,
		},
	}

	moduleBadValue, parseErrors := parser.ParseLibFiles(ctx, sourcesBadValue)
	require.Empty(t, parseErrors)

	inferCtx2 := Context{
		Scope:   Prelude(c2),
		IsAsync: false,
	}

	_, inferErrorsBadValue := c2.InferModule(inferCtx2, moduleBadValue)
	require.NotEmpty(t, inferErrorsBadValue, "Non-exported value should not be accessible via namespace import")
	assert.Contains(t, inferErrorsBadValue[0].Message(), "internalHelper",
		"Error should mention the non-exported value")

	// Test that non-exported types cause errors when accessed
	c3 := NewChecker(ctx)
	err = c3.PackageRegistry.Register("mixed-exports-pkg", mockNs)
	require.NoError(t, err)

	sourcesBadType := []*ast.Source{
		{
			ID:   0,
			Path: "lib/bad_type.esc",
			Contents: `
				import "mixed-exports-pkg" as pkg
				declare val x: pkg.InternalType
			`,
		},
	}

	moduleBadType, parseErrors := parser.ParseLibFiles(ctx, sourcesBadType)
	require.Empty(t, parseErrors)

	inferCtx3 := Context{
		Scope:   Prelude(c3),
		IsAsync: false,
	}

	_, inferErrorsBadType := c3.InferModule(inferCtx3, moduleBadType)
	require.NotEmpty(t, inferErrorsBadType, "Non-exported type should not be accessible via namespace import")
	assert.Contains(t, inferErrorsBadType[0].Message(), "InternalType",
		"Error should mention the non-exported type")
}

// TestNamespaceImportFromSubNamespace verifies importing a sub-namespace from a package
// A package's nested namespace is reached through the namespace the import
// binds, one segment at a time.
func TestNestedNamespaceReachedThroughTheImport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	c := NewChecker(ctx)

	// Create a package with a nested namespace
	mockNs := type_system.NewNamespace()
	subNs := type_system.NewNamespace()
	subNs.Exported = true
	subNs.Values["nestedFunc"] = &type_system.Binding{
		Type:     type_system.NewNumPrimType(nil),
		Mutable:  false,
		Exported: true,
	}
	mockNs.SetNamespace("nested", subNs)

	err := c.PackageRegistry.Register("nested-pkg", mockNs)
	require.NoError(t, err)

	// Verify imports work by using the nested namespace
	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/main.esc",
			Contents: `
				import "nested-pkg" as pkg
				val x = pkg.nested.nestedFunc
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	require.Empty(t, parseErrors)

	inferCtx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	_, inferErrors := c.InferModule(inferCtx, module)

	for _, err := range inferErrors {
		t.Logf("Error: %s", err.Message())
	}
	assert.Empty(t, inferErrors, "a nested namespace should be reachable through the import")
}
