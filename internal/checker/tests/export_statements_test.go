package tests

import (
	"testing"

	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProcessLocalNamedExport_Values verifies that local named exports correctly
// mark values as exported and handle aliasing.
func TestProcessLocalNamedExport_Values(t *testing.T) {
	c := NewChecker()

	// Create package namespace with internal values
	pkgNs := type_system.NewNamespace()
	pkgNs.Values["internalFunc"] = &type_system.Binding{
		Type:     type_system.NewNumPrimType(nil),
		Mutable:  false,
		Exported: false, // Not exported yet
	}
	pkgNs.Values["anotherFunc"] = &type_system.Binding{
		Type:     type_system.NewStrPrimType(nil),
		Mutable:  false,
		Exported: false,
	}

	// Create ParsedTypeDef with local named export
	parsedTypeDef := &ParsedTypeDef{
		NamedExports: []*dts_parser.NamedExportStmt{
			{
				Specifiers: []*dts_parser.ExportSpecifier{
					{
						Local:    dts_parser.NewIdent("internalFunc", DEFAULT_SPAN),
						Exported: dts_parser.NewIdent("publicFunc", DEFAULT_SPAN), // Aliased
					},
					{
						Local:    dts_parser.NewIdent("anotherFunc", DEFAULT_SPAN),
						Exported: dts_parser.NewIdent("anotherFunc", DEFAULT_SPAN), // Same name
					},
				},
				From: "", // Empty means local export
			},
		},
	}

	ctx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	errors := c.ProcessExportStatements(ctx, "/test/index.d.ts", parsedTypeDef, pkgNs)

	assert.Empty(t, errors, "Should process local exports without errors")

	// Check aliased export
	binding, found := pkgNs.Values["publicFunc"]
	assert.True(t, found, "Should have 'publicFunc' export")
	assert.True(t, binding.Exported, "publicFunc should be marked as exported")

	// Check non-aliased export
	binding2, found2 := pkgNs.Values["anotherFunc"]
	assert.True(t, found2, "Should have 'anotherFunc' export")
	assert.True(t, binding2.Exported, "anotherFunc should be marked as exported")
}

// TestProcessLocalNamedExport_Types verifies that local named exports handle type aliases.
func TestProcessLocalNamedExport_Types(t *testing.T) {
	c := NewChecker()

	// Create package namespace with internal types
	pkgNs := type_system.NewNamespace()
	pkgNs.Types["InternalType"] = &type_system.TypeAlias{
		Type:       type_system.NewStrPrimType(nil),
		TypeParams: nil,
		Exported:   false,
	}

	parsedTypeDef := &ParsedTypeDef{
		NamedExports: []*dts_parser.NamedExportStmt{
			{
				Specifiers: []*dts_parser.ExportSpecifier{
					{
						Local:    dts_parser.NewIdent("InternalType", DEFAULT_SPAN),
						Exported: dts_parser.NewIdent("PublicType", DEFAULT_SPAN),
					},
				},
				From: "",
			},
		},
	}

	ctx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	errors := c.ProcessExportStatements(ctx, "/test/index.d.ts", parsedTypeDef, pkgNs)

	assert.Empty(t, errors, "Should process local type exports without errors")

	// Check exported type alias
	typeAlias, found := pkgNs.Types["PublicType"]
	assert.True(t, found, "Should have 'PublicType' type export")
	assert.True(t, typeAlias.Exported, "PublicType should be marked as exported")
}

// TestProcessLocalNamedExport_Namespaces verifies that local named exports handle nested namespaces.
func TestProcessLocalNamedExport_Namespaces(t *testing.T) {
	c := NewChecker()

	// Create package namespace with nested namespace
	pkgNs := type_system.NewNamespace()
	nestedNs := type_system.NewNamespace()
	nestedNs.Values["helper"] = &type_system.Binding{
		Type:     type_system.NewNumPrimType(nil),
		Exported: true,
	}
	pkgNs.Namespaces["internal"] = nestedNs

	parsedTypeDef := &ParsedTypeDef{
		NamedExports: []*dts_parser.NamedExportStmt{
			{
				Specifiers: []*dts_parser.ExportSpecifier{
					{
						Local:    dts_parser.NewIdent("internal", DEFAULT_SPAN),
						Exported: dts_parser.NewIdent("utils", DEFAULT_SPAN),
					},
				},
				From: "",
			},
		},
	}

	ctx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	errors := c.ProcessExportStatements(ctx, "/test/index.d.ts", parsedTypeDef, pkgNs)

	assert.Empty(t, errors, "Should process namespace exports without errors")

	// Check namespace is accessible under new name
	ns, found := pkgNs.Namespaces["utils"]
	assert.True(t, found, "Should have 'utils' namespace export")
	assert.NotNil(t, ns, "Namespace should not be nil")

	// Verify it's the same namespace
	_, hasHelper := ns.Values["helper"]
	assert.True(t, hasHelper, "Exported namespace should contain 'helper' value")
}

// TestProcessLocalNamedExport_NotFound verifies error reporting for missing exports.
func TestProcessLocalNamedExport_NotFound(t *testing.T) {
	c := NewChecker()

	pkgNs := type_system.NewNamespace()

	parsedTypeDef := &ParsedTypeDef{
		NamedExports: []*dts_parser.NamedExportStmt{
			{
				Specifiers: []*dts_parser.ExportSpecifier{
					{
						Local:    dts_parser.NewIdent("doesNotExist", DEFAULT_SPAN),
						Exported: dts_parser.NewIdent("doesNotExist", DEFAULT_SPAN),
					},
				},
				From:     "",
				TypeOnly: false,
			},
		},
	}

	ctx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	errors := c.ProcessExportStatements(ctx, "/test/index.d.ts", parsedTypeDef, pkgNs)

	require.Len(t, errors, 1, "Should have exactly one error")
	assert.Contains(t, errors[0].Message(), "doesNotExist")
	assert.Contains(t, errors[0].Message(), "not found")
}

// TestProcessLocalNamedExport_TypeOnlyIgnoresMissingValues verifies that type-only exports
// don't report errors for missing value bindings.
func TestProcessLocalNamedExport_TypeOnlyIgnoresMissingValues(t *testing.T) {
	c := NewChecker()

	// Namespace with a type but no value of same name
	pkgNs := type_system.NewNamespace()
	pkgNs.Types["MyType"] = &type_system.TypeAlias{
		Type:     type_system.NewStrPrimType(nil),
		Exported: false,
	}

	parsedTypeDef := &ParsedTypeDef{
		NamedExports: []*dts_parser.NamedExportStmt{
			{
				Specifiers: []*dts_parser.ExportSpecifier{
					{
						Local:    dts_parser.NewIdent("MyType", DEFAULT_SPAN),
						Exported: dts_parser.NewIdent("MyType", DEFAULT_SPAN),
					},
				},
				From:     "",
				TypeOnly: true, // Type-only export
			},
		},
	}

	ctx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	errors := c.ProcessExportStatements(ctx, "/test/index.d.ts", parsedTypeDef, pkgNs)

	assert.Empty(t, errors, "Type-only export should not fail when value is missing")

	typeAlias, found := pkgNs.Types["MyType"]
	assert.True(t, found, "Should have 'MyType' type export")
	assert.True(t, typeAlias.Exported, "MyType should be marked as exported")
}

// TestProcessReExport_Values verifies re-exporting values from another module.
// This test uses a relative path pattern that can be resolved.
func TestProcessReExport_Values(t *testing.T) {
	c := NewChecker()

	// Pre-register a dependency package at the resolved path
	// When "./dep" is resolved from "/test/index.d.ts", it becomes "/test/dep.d.ts"
	depNs := type_system.NewNamespace()
	depNs.Values["originalFunc"] = &type_system.Binding{
		Type:     type_system.NewNumPrimType(nil),
		Mutable:  false,
		Exported: true,
	}
	err := c.PackageRegistry.Register("/test/dep.d.ts", depNs)
	require.NoError(t, err)

	// Create target namespace
	pkgNs := type_system.NewNamespace()

	parsedTypeDef := &ParsedTypeDef{
		NamedExports: []*dts_parser.NamedExportStmt{
			{
				Specifiers: []*dts_parser.ExportSpecifier{
					{
						Local:    dts_parser.NewIdent("originalFunc", DEFAULT_SPAN),
						Exported: dts_parser.NewIdent("renamedFunc", DEFAULT_SPAN),
					},
				},
				From: "./dep", // Relative path re-export
			},
		},
	}

	ctx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	errors := c.ProcessExportStatements(ctx, "/test/index.d.ts", parsedTypeDef, pkgNs)

	assert.Empty(t, errors, "Should process re-exports without errors")

	// Check re-exported value
	binding, found := pkgNs.Values["renamedFunc"]
	assert.True(t, found, "Should have 'renamedFunc' re-export")
	assert.True(t, binding.Exported, "renamedFunc should be marked as exported")
}

// TestProcessReExport_Types verifies re-exporting types from another module.
func TestProcessReExport_Types(t *testing.T) {
	c := NewChecker()

	// Pre-register a dependency package at the resolved path
	depNs := type_system.NewNamespace()
	depNs.Types["DepType"] = &type_system.TypeAlias{
		Type:       type_system.NewStrPrimType(nil),
		TypeParams: nil,
		Exported:   true,
	}
	err := c.PackageRegistry.Register("/test/types.d.ts", depNs)
	require.NoError(t, err)

	pkgNs := type_system.NewNamespace()

	parsedTypeDef := &ParsedTypeDef{
		NamedExports: []*dts_parser.NamedExportStmt{
			{
				Specifiers: []*dts_parser.ExportSpecifier{
					{
						Local:    dts_parser.NewIdent("DepType", DEFAULT_SPAN),
						Exported: dts_parser.NewIdent("ReExportedType", DEFAULT_SPAN),
					},
				},
				From: "./types", // Relative path
			},
		},
	}

	ctx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	errors := c.ProcessExportStatements(ctx, "/test/index.d.ts", parsedTypeDef, pkgNs)

	assert.Empty(t, errors, "Should process type re-exports without errors")

	typeAlias, found := pkgNs.Types["ReExportedType"]
	assert.True(t, found, "Should have 'ReExportedType' re-export")
	assert.True(t, typeAlias.Exported, "ReExportedType should be marked as exported")
}

// TestProcessReExport_MissingExport verifies error when re-exporting non-existent item.
func TestProcessReExport_MissingExport(t *testing.T) {
	c := NewChecker()

	// Pre-register an empty dependency package
	depNs := type_system.NewNamespace()
	err := c.PackageRegistry.Register("/test/empty.d.ts", depNs)
	require.NoError(t, err)

	pkgNs := type_system.NewNamespace()

	parsedTypeDef := &ParsedTypeDef{
		NamedExports: []*dts_parser.NamedExportStmt{
			{
				Specifiers: []*dts_parser.ExportSpecifier{
					{
						Local:    dts_parser.NewIdent("missing", DEFAULT_SPAN),
						Exported: dts_parser.NewIdent("missing", DEFAULT_SPAN),
					},
				},
				From:     "./empty", // Relative path
				TypeOnly: false,
			},
		},
	}

	ctx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	errors := c.ProcessExportStatements(ctx, "/test/index.d.ts", parsedTypeDef, pkgNs)

	require.Len(t, errors, 1, "Should have exactly one error")
	assert.Contains(t, errors[0].Message(), "missing")
	assert.Contains(t, errors[0].Message(), "no export named")
}

// TestProcessExportAll_Merge verifies that export * merges all exports from source module.
func TestProcessExportAll_Merge(t *testing.T) {
	c := NewChecker()

	// Pre-register source package with various exports
	srcNs := type_system.NewNamespace()
	srcNs.Values["srcFunc"] = &type_system.Binding{
		Type:     type_system.NewNumPrimType(nil),
		Mutable:  false,
		Exported: true,
	}
	srcNs.Types["SrcType"] = &type_system.TypeAlias{
		Type:       type_system.NewStrPrimType(nil),
		TypeParams: nil,
		Exported:   true,
	}
	err := c.PackageRegistry.Register("/test/source.d.ts", srcNs)
	require.NoError(t, err)

	// Target namespace with existing export (should not be overwritten)
	pkgNs := type_system.NewNamespace()
	pkgNs.Values["existingFunc"] = &type_system.Binding{
		Type:     type_system.NewBoolPrimType(nil),
		Mutable:  false,
		Exported: true,
	}

	parsedTypeDef := &ParsedTypeDef{
		ExportAllStmts: []*dts_parser.ExportAllStmt{
			{
				AsName: nil,        // Plain export *
				From:   "./source", // Relative path
			},
		},
	}

	ctx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	errors := c.ProcessExportStatements(ctx, "/test/index.d.ts", parsedTypeDef, pkgNs)

	assert.Empty(t, errors, "Should process export * without errors")

	// Check merged exports
	srcFunc, found := pkgNs.Values["srcFunc"]
	assert.True(t, found, "Should have merged 'srcFunc'")
	assert.True(t, srcFunc.Exported, "srcFunc should be exported")

	srcType, found := pkgNs.Types["SrcType"]
	assert.True(t, found, "Should have merged 'SrcType'")
	assert.True(t, srcType.Exported, "SrcType should be exported")

	// Existing export should remain unchanged
	existing, found := pkgNs.Values["existingFunc"]
	assert.True(t, found, "Should still have 'existingFunc'")
	assert.True(t, existing.Exported, "existingFunc should remain exported")
}

// TestProcessExportAll_NoOverwrite verifies that export * doesn't overwrite existing exports.
func TestProcessExportAll_NoOverwrite(t *testing.T) {
	c := NewChecker()

	// Source package has a conflicting name
	srcNs := type_system.NewNamespace()
	srcNs.Values["shared"] = &type_system.Binding{
		Type:     type_system.NewStrPrimType(nil), // String type from source
		Mutable:  false,
		Exported: true,
	}
	err := c.PackageRegistry.Register("/test/conflict.d.ts", srcNs)
	require.NoError(t, err)

	// Target namespace already has 'shared' as number
	pkgNs := type_system.NewNamespace()
	pkgNs.Values["shared"] = &type_system.Binding{
		Type:     type_system.NewNumPrimType(nil), // Number type in target
		Mutable:  false,
		Exported: true,
	}

	parsedTypeDef := &ParsedTypeDef{
		ExportAllStmts: []*dts_parser.ExportAllStmt{
			{
				AsName: nil,
				From:   "./conflict", // Relative path
			},
		},
	}

	ctx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	errors := c.ProcessExportStatements(ctx, "/test/index.d.ts", parsedTypeDef, pkgNs)

	assert.Empty(t, errors, "Should not error on conflict (first export wins)")

	// Original type should be preserved (number, not string)
	binding, found := pkgNs.Values["shared"]
	assert.True(t, found, "Should have 'shared'")
	assert.Equal(t, type_system.NewNumPrimType(nil), binding.Type,
		"First export wins - should still be number type")
}

// TestProcessExportAll_AsNamespace verifies export * as ns creates a namespace binding.
func TestProcessExportAll_AsNamespace(t *testing.T) {
	c := NewChecker()

	// Source package
	srcNs := type_system.NewNamespace()
	srcNs.Values["foo"] = &type_system.Binding{
		Type:     type_system.NewNumPrimType(nil),
		Exported: true,
	}
	srcNs.Values["bar"] = &type_system.Binding{
		Type:     type_system.NewStrPrimType(nil),
		Exported: true,
	}
	err := c.PackageRegistry.Register("/test/ns.d.ts", srcNs)
	require.NoError(t, err)

	pkgNs := type_system.NewNamespace()

	parsedTypeDef := &ParsedTypeDef{
		ExportAllStmts: []*dts_parser.ExportAllStmt{
			{
				AsName: dts_parser.NewIdent("myNs", DEFAULT_SPAN),
				From:   "./ns", // Relative path
			},
		},
	}

	ctx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	errors := c.ProcessExportStatements(ctx, "/test/index.d.ts", parsedTypeDef, pkgNs)

	assert.Empty(t, errors, "Should process export * as ns without errors")

	// Should have namespace binding
	ns, found := pkgNs.Namespaces["myNs"]
	assert.True(t, found, "Should have 'myNs' namespace")
	assert.NotNil(t, ns, "Namespace should not be nil")

	// Namespace should contain the exports
	_, hasFoo := ns.Values["foo"]
	assert.True(t, hasFoo, "myNs should contain 'foo'")
	_, hasBar := ns.Values["bar"]
	assert.True(t, hasBar, "myNs should contain 'bar'")
}

// TestProcessExportAsNamespace_UMD verifies the UMD pattern adds to global scope.
func TestProcessExportAsNamespace_UMD(t *testing.T) {
	c := NewChecker()

	// Setup global scope
	preludeScope := Prelude(c)

	// Package namespace with exports
	pkgNs := type_system.NewNamespace()
	pkgNs.Values["create"] = &type_system.Binding{
		Type:     type_system.NewNumPrimType(nil),
		Exported: true,
	}

	parsedTypeDef := &ParsedTypeDef{
		ExportAsNamespace: &dts_parser.ExportAsNamespaceStmt{
			Name: dts_parser.NewIdent("MyLibrary", DEFAULT_SPAN),
		},
	}

	ctx := Context{
		Scope:   preludeScope,
		IsAsync: false,
	}

	errors := c.ProcessExportStatements(ctx, "/test/index.d.ts", parsedTypeDef, pkgNs)

	assert.Empty(t, errors, "Should process export as namespace without errors")

	// Should be in global scope
	globalNs, found := c.GlobalScope.Namespace.Namespaces["MyLibrary"]
	assert.True(t, found, "Should have 'MyLibrary' in global scope")
	assert.Equal(t, pkgNs, globalNs, "Global namespace should reference the package namespace")
}

// TestProcessExportStatements_Combined verifies multiple export types work together.
func TestProcessExportStatements_Combined(t *testing.T) {
	c := NewChecker()

	// Pre-register dependency
	depNs := type_system.NewNamespace()
	depNs.Values["depFunc"] = &type_system.Binding{
		Type:     type_system.NewNumPrimType(nil),
		Exported: true,
	}
	err := c.PackageRegistry.Register("/test/combined-dep.d.ts", depNs)
	require.NoError(t, err)

	// Package with local declaration
	pkgNs := type_system.NewNamespace()
	pkgNs.Values["localFunc"] = &type_system.Binding{
		Type:     type_system.NewStrPrimType(nil),
		Exported: false,
	}

	parsedTypeDef := &ParsedTypeDef{
		// Local export
		NamedExports: []*dts_parser.NamedExportStmt{
			{
				Specifiers: []*dts_parser.ExportSpecifier{
					{
						Local:    dts_parser.NewIdent("localFunc", DEFAULT_SPAN),
						Exported: dts_parser.NewIdent("localFunc", DEFAULT_SPAN),
					},
				},
				From: "",
			},
			// Re-export
			{
				Specifiers: []*dts_parser.ExportSpecifier{
					{
						Local:    dts_parser.NewIdent("depFunc", DEFAULT_SPAN),
						Exported: dts_parser.NewIdent("reexportedFunc", DEFAULT_SPAN),
					},
				},
				From: "./combined-dep", // Relative path
			},
		},
		// UMD export
		ExportAsNamespace: &dts_parser.ExportAsNamespaceStmt{
			Name: dts_parser.NewIdent("CombinedLib", DEFAULT_SPAN),
		},
	}

	ctx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	errors := c.ProcessExportStatements(ctx, "/test/index.d.ts", parsedTypeDef, pkgNs)

	assert.Empty(t, errors, "Should process combined exports without errors")

	// Verify local export
	local, found := pkgNs.Values["localFunc"]
	assert.True(t, found, "Should have 'localFunc'")
	assert.True(t, local.Exported, "localFunc should be exported")

	// Verify re-export
	reexport, found := pkgNs.Values["reexportedFunc"]
	assert.True(t, found, "Should have 'reexportedFunc'")
	assert.True(t, reexport.Exported, "reexportedFunc should be exported")

	// Verify UMD
	globalNs, found := c.GlobalScope.Namespace.Namespaces["CombinedLib"]
	assert.True(t, found, "Should have 'CombinedLib' in global scope")
	assert.NotNil(t, globalNs, "CombinedLib should not be nil")
}

// TestProcessExportStatements_NonExportedFiltered verifies that non-exported items
// from source modules are not accessible via re-exports.
func TestProcessExportStatements_NonExportedFiltered(t *testing.T) {
	c := NewChecker()

	// Source with mixed exports
	srcNs := type_system.NewNamespace()
	srcNs.Values["publicFunc"] = &type_system.Binding{
		Type:     type_system.NewNumPrimType(nil),
		Exported: true,
	}
	srcNs.Values["privateFunc"] = &type_system.Binding{
		Type:     type_system.NewStrPrimType(nil),
		Exported: false, // Not exported
	}
	err := c.PackageRegistry.Register("/test/mixed.d.ts", srcNs)
	require.NoError(t, err)

	pkgNs := type_system.NewNamespace()

	// Try to re-export the private function
	parsedTypeDef := &ParsedTypeDef{
		NamedExports: []*dts_parser.NamedExportStmt{
			{
				Specifiers: []*dts_parser.ExportSpecifier{
					{
						Local:    dts_parser.NewIdent("privateFunc", DEFAULT_SPAN),
						Exported: dts_parser.NewIdent("privateFunc", DEFAULT_SPAN),
					},
				},
				From:     "./mixed", // Relative path
				TypeOnly: false,
			},
		},
	}

	ctx := Context{
		Scope:   Prelude(c),
		IsAsync: false,
	}

	errors := c.ProcessExportStatements(ctx, "/test/index.d.ts", parsedTypeDef, pkgNs)

	// Should error because privateFunc is not exported from source
	require.Len(t, errors, 1, "Should have error for non-exported item")
	assert.Contains(t, errors[0].Message(), "privateFunc")
}
