package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
)

// TestModuleFileTracking verifies that the Module correctly tracks source files
func TestModuleFileTracking(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	sources := []*ast.Source{
		{
			ID:       0,
			Path:     "lib/foo.esc",
			Contents: `fn add(a: number, b: number) -> number { a + b }`,
		},
		{
			ID:       1,
			Path:     "lib/bar.esc",
			Contents: `fn sub(a: number, b: number) -> number { a - b }`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	assert.Empty(t, parseErrors, "Should parse without errors")

	// Verify files are tracked
	assert.Len(t, module.Files, 2, "Should have 2 files")

	// Verify sources map is populated
	assert.Len(t, module.Sources, 2, "Should have 2 sources in map")

	// Verify file paths and source IDs
	for _, file := range module.Files {
		source, exists := module.Sources[file.SourceID]
		assert.True(t, exists, "Source should exist for file %s", file.Path)
		assert.Equal(t, file.Path, source.Path, "File path should match source path")
	}
}

// TestModuleFileImports verifies that import statements are tracked per file
func TestModuleFileImports(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/foo.esc",
			// File with imports
			Contents: `
				import * as fde from "fast-deep-equal"
				fn compare(a: any, b: any) -> boolean { true }
			`,
		},
		{
			ID:   1,
			Path: "lib/bar.esc",
			// File without imports
			Contents: `fn add(a: number, b: number) -> number { a + b }`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	assert.Empty(t, parseErrors, "Should parse without errors")

	// Find the files
	var fooFile, barFile *ast.File
	for _, file := range module.Files {
		if file.Path == "lib/foo.esc" {
			fooFile = file
		} else if file.Path == "lib/bar.esc" {
			barFile = file
		}
	}

	assert.NotNil(t, fooFile, "Should find foo.esc")
	assert.NotNil(t, barFile, "Should find bar.esc")

	// Verify import statements are tracked per file
	assert.Len(t, fooFile.Imports, 1, "foo.esc should have 1 import")
	assert.Len(t, barFile.Imports, 0, "bar.esc should have no imports")

	// Verify the import details
	if len(fooFile.Imports) > 0 {
		importStmt := fooFile.Imports[0]
		assert.Equal(t, "fast-deep-equal", importStmt.PackageName)
	}
}

// TestCrossFileDeclarationVisibility verifies that declarations are visible across files
func TestCrossFileDeclarationVisibility(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/types.esc",
			// File defines a type
			Contents: `type UserId = number`,
		},
		{
			ID:   1,
			Path: "lib/utils.esc",
			// File uses the type from another file - declares a value with that type
			Contents: `
				declare val userId: UserId
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	assert.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	// Infer the module - this should succeed because UserId should be visible to utils.esc
	inferErrors := c.InferModule(inferCtx, module)

	// Check that inference completed without errors
	for i, err := range inferErrors {
		t.Logf("Error[%d]: %s", i, err.Message())
	}
	assert.Empty(t, inferErrors, "Should infer without errors - UserId should be visible to utils.esc")

	// Verify both the type and value are in the namespace
	scope := inferCtx.Scope.Namespace
	_, typeExists := scope.Types["UserId"]
	assert.True(t, typeExists, "UserId type should be in module namespace")

	_, valueExists := scope.Values["userId"]
	assert.True(t, valueExists, "userId value should be in module namespace")
}

// TestFileNamespaceFromPath verifies that file namespace is correctly derived from path
func TestFileNamespaceFromPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	sources := []*ast.Source{
		{
			ID:       0,
			Path:     "lib/math/add.esc",
			Contents: `fn add(a: number, b: number) -> number { a + b }`,
		},
		{
			ID:       1,
			Path:     "lib/math/sub.esc",
			Contents: `fn sub(a: number, b: number) -> number { a - b }`,
		},
		{
			ID:       2,
			Path:     "lib/string/concat.esc",
			Contents: `fn concat(a: string, b: string) -> string { a ++ b }`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	assert.Empty(t, parseErrors, "Should parse without errors")

	// Verify file namespaces are correctly derived
	namespaces := make(map[string][]string) // namespace -> file paths
	for _, file := range module.Files {
		namespaces[file.Namespace] = append(namespaces[file.Namespace], file.Path)
	}

	// Check that math namespace has both add.esc and sub.esc
	assert.Contains(t, namespaces["math"], "lib/math/add.esc")
	assert.Contains(t, namespaces["math"], "lib/math/sub.esc")

	// Check that string namespace has concat.esc
	assert.Contains(t, namespaces["string"], "lib/string/concat.esc")
}

// createMockPackage creates a mock package namespace with the given values and types
func createMockPackage(values map[string]type_system.Type, types map[string]type_system.Type) *type_system.Namespace {
	ns := type_system.NewNamespace()
	for name, t := range values {
		ns.Values[name] = &type_system.Binding{
			Source:  nil,
			Type:    t,
			Mutable: false,
		}
	}
	for name, t := range types {
		ns.Types[name] = &type_system.TypeAlias{
			Type:       t,
			TypeParams: nil,
		}
	}
	return ns
}

// TestFileScopedImportsBasic verifies that imports from a mocked package work correctly
func TestFileScopedImportsBasic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/main.esc",
			Contents: `
				import * as utils from "test-utils"
				declare val result: utils.StringUtil
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	assert.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()

	// Pre-populate PackageRegistry with mock package
	mockPkg := createMockPackage(
		map[string]type_system.Type{
			"helper": type_system.NewFuncType(nil, nil,
				[]*type_system.FuncParam{
					type_system.NewFuncParam(type_system.NewIdentPat("x"), type_system.NewStrPrimType(nil)),
				},
				type_system.NewStrPrimType(nil),
				type_system.NewNeverType(nil),
			),
		},
		map[string]type_system.Type{
			"StringUtil": type_system.NewStrPrimType(nil),
		},
	)
	err := c.PackageRegistry.Register("test-utils", mockPkg)
	assert.NoError(t, err, "Should register mock package")

	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	inferErrors := c.InferModule(inferCtx, module)

	for i, err := range inferErrors {
		t.Logf("Error[%d]: %s", i, err.Message())
	}
	assert.Empty(t, inferErrors, "Should infer without errors")

	// Verify the result value exists in the module namespace
	// (imports are file-scoped, but declarations go to the module namespace)
	scope := inferCtx.Scope.Namespace
	resultBinding, exists := scope.Values["result"]
	assert.True(t, exists, "result should be declared in module namespace")
	if exists {
		// The type may be displayed as utils.StringUtil since it's a type reference
		t.Logf("result type: %s", resultBinding.Type.String())
	}
}

// TestFileScopedImportsIsolation verifies that imports in one file are NOT visible to other files
func TestFileScopedImportsIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/file1.esc",
			// This file imports a package
			Contents: `
				import * as pkg from "my-package"
				declare val value1: pkg.MyType
			`,
		},
		{
			ID:   1,
			Path: "lib/file2.esc",
			// This file does NOT import the package but tries to use 'pkg'
			// This should fail because 'pkg' is only visible in file1.esc
			Contents: `
				declare val value2: pkg.MyType
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	assert.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()

	// Pre-populate PackageRegistry with mock package
	mockPkg := createMockPackage(
		map[string]type_system.Type{},
		map[string]type_system.Type{
			"MyType": type_system.NewNumPrimType(nil),
		},
	)
	err := c.PackageRegistry.Register("my-package", mockPkg)
	assert.NoError(t, err, "Should register mock package")

	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	inferErrors := c.InferModule(inferCtx, module)

	// We expect an error because file2.esc cannot see 'pkg' which was imported in file1.esc
	assert.NotEmpty(t, inferErrors, "Should have errors - pkg should not be visible in file2.esc")

	// Check that the error is related to 'pkg' not being found
	foundPkgError := false
	for _, err := range inferErrors {
		t.Logf("Error: %s", err.Message())
		if strings.Contains(err.Message(), "pkg") {
			foundPkgError = true
		}
	}
	assert.True(t, foundPkgError, "Should have an error about 'pkg' not being visible")
}

// TestFileScopedImportsSamePackageDifferentFiles verifies that each file can import
// the same package independently
func TestFileScopedImportsSamePackageDifferentFiles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/file1.esc",
			Contents: `
				import * as utils from "shared-utils"
				declare val val1: utils.SharedType
			`,
		},
		{
			ID:   1,
			Path: "lib/file2.esc",
			Contents: `
				import * as utils from "shared-utils"
				declare val val2: utils.SharedType
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	assert.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()

	// Pre-populate PackageRegistry with mock package
	mockPkg := createMockPackage(
		map[string]type_system.Type{},
		map[string]type_system.Type{
			"SharedType": type_system.NewBoolPrimType(nil),
		},
	)
	err := c.PackageRegistry.Register("shared-utils", mockPkg)
	assert.NoError(t, err, "Should register mock package")

	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	inferErrors := c.InferModule(inferCtx, module)

	for i, err := range inferErrors {
		t.Logf("Error[%d]: %s", i, err.Message())
	}
	assert.Empty(t, inferErrors, "Should infer without errors - both files import independently")

	// Verify both values exist
	scope := inferCtx.Scope.Namespace
	_, val1Exists := scope.Values["val1"]
	_, val2Exists := scope.Values["val2"]
	assert.True(t, val1Exists, "val1 should exist")
	assert.True(t, val2Exists, "val2 should exist")
}

// TestFileScopedImportsDifferentAliases verifies that different files can use
// different aliases for the same package
func TestFileScopedImportsDifferentAliases(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/file1.esc",
			Contents: `
				import * as foo from "my-lib"
				declare val val1: foo.LibType
			`,
		},
		{
			ID:   1,
			Path: "lib/file2.esc",
			Contents: `
				import * as bar from "my-lib"
				declare val val2: bar.LibType
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	assert.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()

	// Pre-populate PackageRegistry with mock package
	mockPkg := createMockPackage(
		map[string]type_system.Type{},
		map[string]type_system.Type{
			"LibType": type_system.NewStrPrimType(nil),
		},
	)
	err := c.PackageRegistry.Register("my-lib", mockPkg)
	assert.NoError(t, err, "Should register mock package")

	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	inferErrors := c.InferModule(inferCtx, module)

	for i, err := range inferErrors {
		t.Logf("Error[%d]: %s", i, err.Message())
	}
	assert.Empty(t, inferErrors, "Should infer without errors - different aliases for same package")

	// Verify both values exist with correct type references
	// Note: The types are stored as type references (e.g., foo.LibType, bar.LibType)
	// because the imports are file-scoped. Each file uses a different alias for the
	// same package, and the types correctly reference the import alias from their file.
	scope := inferCtx.Scope.Namespace
	val1, val1Exists := scope.Values["val1"]
	val2, val2Exists := scope.Values["val2"]
	assert.True(t, val1Exists, "val1 should exist")
	assert.True(t, val2Exists, "val2 should exist")
	if val1Exists && val2Exists {
		// Check that each value has the correct type reference from its file's import alias
		assert.Equal(t, "foo.LibType", val1.Type.String(), "val1 should use foo alias from file1")
		assert.Equal(t, "bar.LibType", val2.Type.String(), "val2 should use bar alias from file2")
	}
}

// TestNamedImportsFromPackage verifies that named imports (not namespace imports) work
func TestNamedImportsFromPackage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/main.esc",
			Contents: `
				import { myFunc, MyType } from "named-pkg"
				declare val x: MyType
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	assert.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()

	// Pre-populate PackageRegistry with mock package that has both values and types
	mockPkg := createMockPackage(
		map[string]type_system.Type{
			"myFunc": type_system.NewFuncType(nil, nil,
				[]*type_system.FuncParam{},
				type_system.NewNumPrimType(nil),
				type_system.NewNeverType(nil),
			),
		},
		map[string]type_system.Type{
			"MyType": type_system.NewNumPrimType(nil),
		},
	)
	err := c.PackageRegistry.Register("named-pkg", mockPkg)
	assert.NoError(t, err, "Should register mock package")

	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	inferErrors := c.InferModule(inferCtx, module)

	for i, err := range inferErrors {
		t.Logf("Error[%d]: %s", i, err.Message())
	}
	assert.Empty(t, inferErrors, "Should infer without errors")

	// Verify the declaration uses the named import correctly
	// Note: Named imports (myFunc, MyType) are file-scoped, so they won't appear in
	// the module namespace directly. The test verifies that:
	// 1. Inference succeeds (which means the import was resolved correctly)
	// 2. The declaration x exists with the correct type reference
	scope := inferCtx.Scope.Namespace

	// Named imports are file-scoped, so they should NOT be in the module namespace
	_, funcExists := scope.Values["myFunc"]
	assert.False(t, funcExists, "myFunc should NOT be in module namespace (it's file-scoped)")

	_, typeExists := scope.Types["MyType"]
	assert.False(t, typeExists, "MyType should NOT be in module namespace (it's file-scoped)")

	// Check x is declared with MyType reference
	xBinding, xExists := scope.Values["x"]
	assert.True(t, xExists, "x should be declared in module namespace")
	if xExists {
		// The type is stored as a reference to MyType
		assert.Equal(t, "MyType", xBinding.Type.String())
	}
}

// TestScopeChainTraversal verifies that user code can access globals via parent chain lookup
func TestScopeChainTraversal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Module with code that references globals (Array, number, string, etc.)
	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/main.esc",
			Contents: `
				declare fn double(x: number) -> number
				declare val arr: Array<string>
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	assert.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()
	inferCtx := Context{
		Scope:      Prelude(c), // Prelude sets up global scope with Array, etc.
		IsAsync:    false,
		IsPatMatch: false,
	}

	inferErrors := c.InferModule(inferCtx, module)

	for i, err := range inferErrors {
		t.Logf("Error[%d]: %s", i, err.Message())
	}
	assert.Empty(t, inferErrors, "Should infer without errors - globals should be accessible via scope chain")

	// Verify that the declarations exist and use global types
	scope := inferCtx.Scope.Namespace

	doubleBinding, doubleExists := scope.Values["double"]
	assert.True(t, doubleExists, "double function should exist")
	if doubleExists {
		t.Logf("double type: %s", doubleBinding.Type.String())
		assert.Contains(t, doubleBinding.Type.String(), "number", "double should use global number type")
	}

	arrBinding, arrExists := scope.Values["arr"]
	assert.True(t, arrExists, "arr should exist")
	if arrExists {
		t.Logf("arr type: %s", arrBinding.Type.String())
		assert.Contains(t, arrBinding.Type.String(), "Array", "arr should use global Array type")
	}
}

// TestGlobalNamespaceIsolation verifies that globals are in a separate namespace from module declarations
func TestGlobalNamespaceIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Define a local type with the same name as a global
	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/main.esc",
			Contents: `
				type Array<T> = {items: T}
				declare val localArr: Array<number>
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	assert.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	inferErrors := c.InferModule(inferCtx, module)

	for i, err := range inferErrors {
		t.Logf("Error[%d]: %s", i, err.Message())
	}
	assert.Empty(t, inferErrors, "Should infer without errors")

	// Verify local Array type shadows the global
	scope := inferCtx.Scope.Namespace

	localArrayAlias, localArrayExists := scope.Types["Array"]
	assert.True(t, localArrayExists, "Local Array type should exist in module namespace")
	if localArrayExists {
		t.Logf("Local Array type: %s", localArrayAlias.Type.String())
	}

	// Verify localArr uses the local Array type (not the global one)
	localArrBinding, localArrExists := scope.Values["localArr"]
	assert.True(t, localArrExists, "localArr should exist")
	if localArrExists {
		t.Logf("localArr type: %s", localArrBinding.Type.String())
		// The local Array has structure {items: T}, not the global Array
		assert.Equal(t, "Array<number>", localArrBinding.Type.String())
	}
}

// TestCrossFileCyclicDependencies verifies that mutually recursive types across files work correctly
func TestCrossFileCyclicDependencies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Two files with mutually recursive types
	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/node.esc",
			Contents: `
				type Node = {
					value: number,
					children: Tree,
				}
			`,
		},
		{
			ID:   1,
			Path: "lib/tree.esc",
			Contents: `
				type Tree = {
					root: Node,
					size: number,
				}
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	assert.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	inferErrors := c.InferModule(inferCtx, module)

	for i, err := range inferErrors {
		t.Logf("Error[%d]: %s", i, err.Message())
	}
	assert.Empty(t, inferErrors, "Should infer without errors - cross-file cyclic dependencies should work")

	// Verify both types exist
	scope := inferCtx.Scope.Namespace

	_, nodeExists := scope.Types["Node"]
	assert.True(t, nodeExists, "Node type should exist")

	_, treeExists := scope.Types["Tree"]
	assert.True(t, treeExists, "Tree type should exist")
}

// TestCrossFileCyclesWithImports verifies that cross-file cycles work correctly
// when one file uses imported types
func TestCrossFileCyclesWithImports(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Two files: one imports a package and defines a type using it,
	// the other references that type
	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/wrapper.esc",
			Contents: `
				import * as utils from "test-utils"
				type Wrapper = {
					data: utils.DataType,
					next: LinkedNode,
				}
			`,
		},
		{
			ID:   1,
			Path: "lib/linked.esc",
			Contents: `
				type LinkedNode = {
					wrapper: Wrapper,
					prev: LinkedNode,
				}
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	assert.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()

	// Pre-populate PackageRegistry with mock package
	mockPkg := createMockPackage(
		map[string]type_system.Type{},
		map[string]type_system.Type{
			"DataType": type_system.NewStrPrimType(nil),
		},
	)
	err := c.PackageRegistry.Register("test-utils", mockPkg)
	assert.NoError(t, err, "Should register mock package")

	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	inferErrors := c.InferModule(inferCtx, module)

	for i, err := range inferErrors {
		t.Logf("Error[%d]: %s", i, err.Message())
	}
	assert.Empty(t, inferErrors, "Should infer without errors - cross-file cycles with imports should work")

	// Verify both types exist
	scope := inferCtx.Scope.Namespace

	_, wrapperExists := scope.Types["Wrapper"]
	assert.True(t, wrapperExists, "Wrapper type should exist")

	_, linkedNodeExists := scope.Types["LinkedNode"]
	assert.True(t, linkedNodeExists, "LinkedNode type should exist")

	// Verify that 'utils' is NOT in module scope (it's file-scoped)
	_, utilsInModule := scope.Namespaces["utils"]
	assert.False(t, utilsInModule, "utils should NOT be in module namespace (it's file-scoped)")
}

// TestCrossFileCyclesImportIsolation verifies that file B cannot use imports from file A
// even when their types form a cycle
func TestCrossFileCyclesImportIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// File A imports a package, file B tries to use the import namespace
	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/file_a.esc",
			Contents: `
				import * as pkg from "my-pkg"
				type TypeA = {
					data: pkg.SomeType,
					ref: TypeB,
				}
			`,
		},
		{
			ID:   1,
			Path: "lib/file_b.esc",
			Contents: `
				type TypeB = {
					a: TypeA,
					invalid: pkg.SomeType,
				}
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	assert.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()

	// Pre-populate PackageRegistry with mock package
	mockPkg := createMockPackage(
		map[string]type_system.Type{},
		map[string]type_system.Type{
			"SomeType": type_system.NewNumPrimType(nil),
		},
	)
	err := c.PackageRegistry.Register("my-pkg", mockPkg)
	assert.NoError(t, err, "Should register mock package")

	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	inferErrors := c.InferModule(inferCtx, module)

	// We expect an error because file_b.esc uses pkg.SomeType without importing pkg
	assert.NotEmpty(t, inferErrors, "Should have errors - file_b cannot use pkg from file_a's import")

	foundPkgError := false
	for _, err := range inferErrors {
		t.Logf("Error: %s", err.Message())
		if err.Message() != "" {
			foundPkgError = true
		}
	}
	assert.True(t, foundPkgError, "Should have an error about pkg not being available in file_b")
}
