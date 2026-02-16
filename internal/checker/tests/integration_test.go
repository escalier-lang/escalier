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
	"github.com/stretchr/testify/require"
)

// =============================================================================
// End-to-End Integration Tests
// These tests exercise the complete system with all features working together:
// - Global namespace separation
// - Package registry and imports
// - Local shadowing of globals
// - globalThis access to shadowed globals
// - Qualified package access
// - File-scoped imports
// =============================================================================

// TestE2E_FullWorkflow tests the complete workflow from the implementation plan:
// load globals -> import multiple packages -> define local types that shadow globals
// -> access shadowed globals via globalThis -> use qualified package access
func TestE2E_FullWorkflow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create a multi-file module that exercises all features
	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/types.esc",
			Contents: `
				// Local type that shadows the global Array type
				type Array<T> = {
					items: T,
					isLocalArray: boolean,
				}

				// Local type that shadows global Number
				type Number = {
					value: number,
					isLocalNumber: boolean,
				}
			`,
		},
		{
			ID:   1,
			Path: "lib/utils.esc",
			Contents: `
				// Import a package and use qualified access
				import * as lodash from "lodash"
				import * as ramda from "ramda"

				// Use local Array type (shadowed)
				declare val localArr: Array<string>

				// Use global Array via globalThis
				declare val globalArr: globalThis.Array<number>

				// Use local Number type (shadowed)
				declare val localNum: Number

				// Use package types via qualified access
				declare val lodashResult: lodash.MapResult<number>
				declare val ramdaResult: ramda.PipeResult<string>
			`,
		},
		{
			ID:   2,
			Path: "lib/main.esc",
			Contents: `
				// This file doesn't import lodash/ramda - verifies file-scoped imports
				// But it can access module-level types (Array, Number) defined in types.esc

				// Use the shadowed types from types.esc
				declare val mainLocalArr: Array<number>
				declare val mainLocalNum: Number

				// Access globals via globalThis
				declare val mainGlobalArr: globalThis.Array<string>
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	require.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()

	// IMPORTANT: Call Prelude FIRST, then register packages.
	// Prelude caches the global scope and package registry, and calling it
	// after registering packages would overwrite them.
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	// Register mock packages to simulate npm packages
	lodashNs := createMockPackage(
		map[string]type_system.Type{
			"map": type_system.NewFuncType(nil, nil,
				[]*type_system.FuncParam{
					type_system.NewFuncParam(type_system.NewIdentPat("arr"), type_system.NewAnyType(nil)),
					type_system.NewFuncParam(type_system.NewIdentPat("fn"), type_system.NewAnyType(nil)),
				},
				type_system.NewAnyType(nil),
				type_system.NewNeverType(nil),
			),
		},
		map[string]type_system.Type{
			"MapResult": type_system.NewAnyType(nil),
		},
	)

	ramdaNs := createMockPackage(
		map[string]type_system.Type{
			"pipe": type_system.NewFuncType(nil, nil,
				[]*type_system.FuncParam{},
				type_system.NewAnyType(nil),
				type_system.NewNeverType(nil),
			),
		},
		map[string]type_system.Type{
			"PipeResult": type_system.NewAnyType(nil),
		},
	)

	require.NoError(t, c.PackageRegistry.Register("lodash", lodashNs))
	require.NoError(t, c.PackageRegistry.Register("ramda", ramdaNs))

	inferErrors := c.InferModule(inferCtx, module)

	// Log any errors for debugging
	for i, err := range inferErrors {
		t.Logf("Error[%d]: %s", i, err.Message())
	}

	assert.Empty(t, inferErrors, "Should infer without errors")

	// Verify the module namespace has all expected declarations
	scope := inferCtx.Scope.Namespace

	// Verify local shadowing types exist
	_, arrayExists := scope.Types["Array"]
	assert.True(t, arrayExists, "Local Array type should exist")

	_, numberExists := scope.Types["Number"]
	assert.True(t, numberExists, "Local Number type should exist")

	// Verify values from utils.esc
	localArrBinding, localArrExists := scope.Values["localArr"]
	assert.True(t, localArrExists, "localArr should exist")
	if localArrExists {
		// Should reference the local Array type
		assert.Equal(t, "Array<string>", localArrBinding.Type.String())
	}

	globalArrBinding, globalArrExists := scope.Values["globalArr"]
	assert.True(t, globalArrExists, "globalArr should exist")
	if globalArrExists {
		// Should reference globalThis.Array
		assert.Contains(t, globalArrBinding.Type.String(), "globalThis.Array")
	}

	// Verify values from main.esc
	mainLocalArrBinding, mainLocalArrExists := scope.Values["mainLocalArr"]
	assert.True(t, mainLocalArrExists, "mainLocalArr should exist")
	if mainLocalArrExists {
		// Should reference the local Array type from types.esc
		assert.Equal(t, "Array<number>", mainLocalArrBinding.Type.String())
	}

	mainGlobalArrBinding, mainGlobalArrExists := scope.Values["mainGlobalArr"]
	assert.True(t, mainGlobalArrExists, "mainGlobalArr should exist")
	if mainGlobalArrExists {
		// Should reference globalThis.Array
		assert.Contains(t, mainGlobalArrBinding.Type.String(), "globalThis.Array")
	}
}

// TestE2E_FileImportIsolation verifies that file A's imports cannot be accessed by file B
func TestE2E_FileImportIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/importer.esc",
			Contents: `
				import * as utils from "my-utils"
				declare val importerValue: utils.UtilType
			`,
		},
		{
			ID:   1,
			Path: "lib/non_importer.esc",
			Contents: `
				// This file does NOT import my-utils but tries to use the 'utils' namespace
				// This should fail because imports are file-scoped
				declare val nonImporterValue: utils.UtilType
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	require.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()

	// IMPORTANT: Call Prelude FIRST, then register packages.
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	// Register mock package
	utilsNs := createMockPackage(
		map[string]type_system.Type{},
		map[string]type_system.Type{
			"UtilType": type_system.NewStrPrimType(nil),
		},
	)
	require.NoError(t, c.PackageRegistry.Register("my-utils", utilsNs))

	inferErrors := c.InferModule(inferCtx, module)

	// Should have an error because non_importer.esc cannot use 'utils' namespace
	require.NotEmpty(t, inferErrors, "Should have errors - file-scoped import isolation violated")

	// Verify the error mentions 'utils' not being found
	foundUtilsError := false
	for _, err := range inferErrors {
		t.Logf("Error: %s", err.Message())
		if strings.Contains(err.Message(), "utils") || strings.Contains(err.Message(), "undefined") {
			foundUtilsError = true
		}
	}
	assert.True(t, foundUtilsError, "Should have an error about 'utils' not being defined")
}

// TestE2E_MultiplePackagesWithSameSymbols tests that different packages can have
// symbols with the same name and they remain isolated
func TestE2E_MultiplePackagesWithSameSymbols(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/main.esc",
			Contents: `
				import * as lodash from "lodash"
				import * as ramda from "ramda"

				// Both packages have 'map' function, but they're isolated via qualified access
				val lodashMap = lodash.map
				val ramdaMap = ramda.map
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	require.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()

	// IMPORTANT: Call Prelude FIRST, then register packages.
	// Prelude caches the global scope and package registry, and calling it
	// after registering packages would overwrite them.
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	// Both packages have a 'map' function but with different signatures
	lodashNs := createMockPackage(
		map[string]type_system.Type{
			"map": type_system.NewFuncType(nil, nil,
				[]*type_system.FuncParam{
					type_system.NewFuncParam(type_system.NewIdentPat("arr"), type_system.NewAnyType(nil)),
					type_system.NewFuncParam(type_system.NewIdentPat("fn"), type_system.NewAnyType(nil)),
				},
				type_system.NewAnyType(nil), // Returns array
				type_system.NewNeverType(nil),
			),
		},
		nil,
	)

	ramdaNs := createMockPackage(
		map[string]type_system.Type{
			"map": type_system.NewFuncType(nil, nil,
				[]*type_system.FuncParam{
					type_system.NewFuncParam(type_system.NewIdentPat("fn"), type_system.NewAnyType(nil)),
					// Ramda's map is curried - takes fn first
				},
				type_system.NewAnyType(nil), // Returns curried function
				type_system.NewNeverType(nil),
			),
		},
		nil,
	)

	require.NoError(t, c.PackageRegistry.Register("lodash", lodashNs))
	require.NoError(t, c.PackageRegistry.Register("ramda", ramdaNs))

	inferErrors := c.InferModule(inferCtx, module)

	for i, err := range inferErrors {
		t.Logf("Error[%d]: %s", i, err.Message())
	}

	assert.Empty(t, inferErrors, "Should infer without errors - packages isolated via qualified access")

	// Verify both bindings exist
	scope := inferCtx.Scope.Namespace
	lodashMapBinding, lodashMapExists := scope.Values["lodashMap"]
	ramdaMapBinding, ramdaMapExists := scope.Values["ramdaMap"]

	assert.True(t, lodashMapExists, "lodashMap should exist")
	assert.True(t, ramdaMapExists, "ramdaMap should exist")

	// Both should be function types
	if lodashMapExists && ramdaMapExists {
		_, isLodashFunc := type_system.Prune(lodashMapBinding.Type).(*type_system.FuncType)
		_, isRamdaFunc := type_system.Prune(ramdaMapBinding.Type).(*type_system.FuncType)
		assert.True(t, isLodashFunc, "lodashMap should be a function")
		assert.True(t, isRamdaFunc, "ramdaMap should be a function")
	}
}

// TestE2E_GlobalThisBypassesShadowing verifies that globalThis always accesses
// the global namespace even when locals shadow globals
func TestE2E_GlobalThisBypassesShadowing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/main.esc",
			Contents: `
				// Shadow several common globals
				type Array<T> = { customArray: T }
				type Promise<T> = { customPromise: T }
				type Number = { customNumber: number }
				type String = { customString: string }

				// Use shadowed types (local definitions)
				declare val localArray: Array<number>
				declare val localPromise: Promise<string>

				// Use global types via globalThis
				declare val globalArray: globalThis.Array<number>
				declare val globalPromise: globalThis.Promise<string>

				// Access globalThis value
				val globalSymbol = globalThis.Symbol.iterator
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	require.Empty(t, parseErrors, "Should parse without errors")

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

	scope := inferCtx.Scope.Namespace

	// Verify local types shadow globals
	localArrayBinding, localArrayExists := scope.Values["localArray"]
	assert.True(t, localArrayExists, "localArray should exist")
	if localArrayExists {
		assert.Equal(t, "Array<number>", localArrayBinding.Type.String())
	}

	// Verify globalThis types access globals
	globalArrayBinding, globalArrayExists := scope.Values["globalArray"]
	assert.True(t, globalArrayExists, "globalArray should exist")
	if globalArrayExists {
		typeStr := globalArrayBinding.Type.String()
		assert.Contains(t, typeStr, "globalThis.Array", "Should reference globalThis.Array")
	}

	// Verify globalThis.Symbol.iterator works
	globalSymbolBinding, globalSymbolExists := scope.Values["globalSymbol"]
	assert.True(t, globalSymbolExists, "globalSymbol should exist")
	if globalSymbolExists {
		symType := type_system.Prune(globalSymbolBinding.Type)
		_, isUniqueSymbol := symType.(*type_system.UniqueSymbolType)
		assert.True(t, isUniqueSymbol, "globalSymbol should be a unique symbol type, got %T", symType)
	}
}

// TestE2E_CrossFileCyclicTypesWithImports tests complex cyclic type dependencies
// across files where one file uses package imports
func TestE2E_CrossFileCyclicTypesWithImports(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/node.esc",
			Contents: `
				import * as utils from "tree-utils"

				// Node references Tree (from tree.esc) and uses imported package
				type Node<T> = {
					value: T,
					metadata: utils.Metadata,
					children: Tree<T>,
				}
			`,
		},
		{
			ID:   1,
			Path: "lib/tree.esc",
			Contents: `
				// Tree references Node (from node.esc) - cyclic dependency
				// This file doesn't import tree-utils, but can reference Node which does
				type Tree<T> = {
					root: Node<T>,
					size: number,
				}
			`,
		},
		{
			ID:   2,
			Path: "lib/forest.esc",
			Contents: `
				import * as utils from "tree-utils"

				// Forest uses both Node and Tree (cross-file types)
				// and has its own import of tree-utils
				type Forest<T> = {
					trees: globalThis.Array<Tree<T>>,
					stats: utils.Metadata,
				}

				declare val myForest: Forest<number>
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	require.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()

	// IMPORTANT: Call Prelude FIRST, then register packages.
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	// Register mock package
	treeUtilsNs := createMockPackage(
		map[string]type_system.Type{},
		map[string]type_system.Type{
			"Metadata": type_system.NewAnyType(nil), // Simplified
		},
	)
	require.NoError(t, c.PackageRegistry.Register("tree-utils", treeUtilsNs))

	inferErrors := c.InferModule(inferCtx, module)

	for i, err := range inferErrors {
		t.Logf("Error[%d]: %s", i, err.Message())
	}

	assert.Empty(t, inferErrors, "Should infer without errors - cross-file cycles with imports should work")

	// Verify all types exist
	scope := inferCtx.Scope.Namespace
	_, nodeExists := scope.Types["Node"]
	_, treeExists := scope.Types["Tree"]
	_, forestExists := scope.Types["Forest"]

	assert.True(t, nodeExists, "Node type should exist")
	assert.True(t, treeExists, "Tree type should exist")
	assert.True(t, forestExists, "Forest type should exist")

	// Verify myForest value exists
	_, myForestExists := scope.Values["myForest"]
	assert.True(t, myForestExists, "myForest should exist")
}

// TestE2E_NamedImportsWithAliases tests named imports with aliasing across multiple files
func TestE2E_NamedImportsWithAliases(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/file1.esc",
			Contents: `
				// Named imports with aliases
				import { Helper as H1, HelperType as HT1 } from "my-pkg"
				val helper1 = H1
				declare val typed1: HT1
			`,
		},
		{
			ID:   1,
			Path: "lib/file2.esc",
			Contents: `
				// Same named imports but different aliases (file-scoped)
				import { Helper as H2, HelperType as HT2 } from "my-pkg"
				val helper2 = H2
				declare val typed2: HT2
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	require.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()

	// IMPORTANT: Call Prelude FIRST, then register packages.
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	// Register mock package
	mockNs := createMockPackage(
		map[string]type_system.Type{
			"Helper": type_system.NewFuncType(nil, nil,
				[]*type_system.FuncParam{},
				type_system.NewStrPrimType(nil),
				type_system.NewNeverType(nil),
			),
		},
		map[string]type_system.Type{
			"HelperType": type_system.NewNumPrimType(nil),
		},
	)
	require.NoError(t, c.PackageRegistry.Register("my-pkg", mockNs))

	inferErrors := c.InferModule(inferCtx, module)

	for i, err := range inferErrors {
		t.Logf("Error[%d]: %s", i, err.Message())
	}

	assert.Empty(t, inferErrors, "Should infer without errors")

	scope := inferCtx.Scope.Namespace

	// Verify all values exist
	_, helper1Exists := scope.Values["helper1"]
	_, helper2Exists := scope.Values["helper2"]
	_, typed1Exists := scope.Values["typed1"]
	_, typed2Exists := scope.Values["typed2"]

	assert.True(t, helper1Exists, "helper1 should exist")
	assert.True(t, helper2Exists, "helper2 should exist")
	assert.True(t, typed1Exists, "typed1 should exist")
	assert.True(t, typed2Exists, "typed2 should exist")

	// The aliases (H1, H2, HT1, HT2) should NOT be in the module namespace
	// because they're file-scoped imports
	_, h1Exists := scope.Values["H1"]
	_, h2Exists := scope.Values["H2"]
	assert.False(t, h1Exists, "H1 alias should NOT be in module namespace")
	assert.False(t, h2Exists, "H2 alias should NOT be in module namespace")
}

// TestE2E_SubpathImportsIsolation tests that subpath imports are separate entries
func TestE2E_SubpathImportsIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/main.esc",
			Contents: `
				// Import both main package and subpath
				import * as lodash from "lodash"
				import * as fp from "lodash/fp"

				// Use values from each - they should be different
				val mainMap = lodash.map
				val fpMap = fp.map
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	require.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()

	// IMPORTANT: Call Prelude FIRST, then register packages.
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	// Main lodash
	mainNs := createMockPackage(
		map[string]type_system.Type{
			"map": type_system.NewFuncType(nil, nil,
				[]*type_system.FuncParam{
					type_system.NewFuncParam(type_system.NewIdentPat("collection"), type_system.NewAnyType(nil)),
					type_system.NewFuncParam(type_system.NewIdentPat("iteratee"), type_system.NewAnyType(nil)),
				},
				type_system.NewAnyType(nil),
				type_system.NewNeverType(nil),
			),
		},
		nil,
	)

	// lodash/fp - curried, data-last
	fpNs := createMockPackage(
		map[string]type_system.Type{
			"map": type_system.NewFuncType(nil, nil,
				[]*type_system.FuncParam{
					type_system.NewFuncParam(type_system.NewIdentPat("iteratee"), type_system.NewAnyType(nil)),
				},
				type_system.NewAnyType(nil), // Returns curried function
				type_system.NewNeverType(nil),
			),
		},
		nil,
	)

	require.NoError(t, c.PackageRegistry.Register("lodash", mainNs))
	require.NoError(t, c.PackageRegistry.Register("lodash/fp", fpNs))

	inferErrors := c.InferModule(inferCtx, module)

	for i, err := range inferErrors {
		t.Logf("Error[%d]: %s", i, err.Message())
	}

	assert.Empty(t, inferErrors, "Should infer without errors")

	scope := inferCtx.Scope.Namespace

	mainMapBinding, mainMapExists := scope.Values["mainMap"]
	fpMapBinding, fpMapExists := scope.Values["fpMap"]

	assert.True(t, mainMapExists, "mainMap should exist")
	assert.True(t, fpMapExists, "fpMap should exist")

	// Both should be functions
	if mainMapExists && fpMapExists {
		mainMapType := type_system.Prune(mainMapBinding.Type)
		fpMapType := type_system.Prune(fpMapBinding.Type)

		mainFuncType, isMainFunc := mainMapType.(*type_system.FuncType)
		fpFuncType, isFpFunc := fpMapType.(*type_system.FuncType)

		assert.True(t, isMainFunc, "mainMap should be a function")
		assert.True(t, isFpFunc, "fpMap should be a function")

		// Main lodash.map takes 2 params, fp.map takes 1 (curried)
		if isMainFunc && isFpFunc {
			assert.Equal(t, 2, len(mainFuncType.Params), "lodash.map should have 2 params")
			assert.Equal(t, 1, len(fpFuncType.Params), "lodash/fp.map should have 1 param (curried)")
		}
	}
}

// =============================================================================
// Complex Project Simulation Tests
// These tests simulate realistic project scenarios with multiple files,
// packages, and complex type relationships
// =============================================================================

// TestE2E_ComplexProjectSimulation simulates a realistic project with:
// - Multiple source files
// - Multiple npm package dependencies
// - Shared types across files
// - Package isolation via qualified access
// - Local shadowing of globals
// - Cross-file cyclic type dependencies
// Note: All files are in the same directory (lib/) because declarations
// are only shared within the same namespace (directory).
func TestE2E_ComplexProjectSimulation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Simulate a project with models, services, and utilities
	// All files in same directory for shared namespace
	sources := []*ast.Source{
		// Shared types
		{
			ID:   0,
			Path: "lib/base.esc",
			Contents: `
				// Base model types used throughout the project
				type ID = string
				type Timestamp = number

				type BaseModel = {
					id: ID,
					createdAt: Timestamp,
					updatedAt: Timestamp,
				}
			`,
		},
		// User model
		{
			ID:   1,
			Path: "lib/user.esc",
			Contents: `
				import * as validator from "validator"

				// User extends BaseModel (cross-file reference)
				type User = BaseModel & {
					email: string,
					name: string,
					posts: globalThis.Array<Post>,
				}

				// Uses imported validator types
				type UserValidation = {
					emailValid: validator.ValidationResult,
					nameValid: validator.ValidationResult,
				}
			`,
		},
		// Post model
		{
			ID:   2,
			Path: "lib/post.esc",
			Contents: `
				// Post references User (cyclic dependency with user.esc)
				type Post = BaseModel & {
					title: string,
					content: string,
					author: User,
					comments: globalThis.Array<Comment>,
				}

				type Comment = BaseModel & {
					text: string,
					author: User,
					post: Post,
				}
			`,
		},
		// User service
		{
			ID:   3,
			Path: "lib/user_service.esc",
			Contents: `
				import * as db from "database"
				import * as validator from "validator"

				// Service uses models from other files and imported packages
				type UserService = {
					db: db.Connection,
					validator: validator.Validator,
				}

				declare fn createUser(service: UserService, data: User) -> globalThis.Promise<User>
				declare fn getUserById(service: UserService, id: ID) -> globalThis.Promise<User>
			`,
		},
		// Post service
		{
			ID:   4,
			Path: "lib/post_service.esc",
			Contents: `
				import * as db from "database"

				type PostService = {
					db: db.Connection,
				}

				declare fn createPost(service: PostService, data: Post) -> globalThis.Promise<Post>
				declare fn getPostsByUser(service: PostService, userId: ID) -> globalThis.Promise<globalThis.Array<Post>>
			`,
		},
		// Main application
		{
			ID:   5,
			Path: "lib/app.esc",
			Contents: `
				import * as db from "database"
				import * as config from "config"

				// Application configuration
				type App = {
					dbConnection: db.Connection,
					settings: config.Settings,
					userService: UserService,
					postService: PostService,
				}

				declare fn initApp(cfg: config.Settings) -> globalThis.Promise<App>

				// Use a local Array type that shadows global
				type Array<T> = { items: T, length: number }
				declare val customArray: Array<string>
				declare val globalArray: globalThis.Array<string>
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	require.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()

	// IMPORTANT: Call Prelude FIRST, then register packages.
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	// Register mock packages with simple types
	// Using AnyType for complex object types to avoid API complexity
	validatorNs := createMockPackage(
		map[string]type_system.Type{},
		map[string]type_system.Type{
			"ValidationResult": type_system.NewAnyType(nil), // Simplified
			"Validator":        type_system.NewAnyType(nil), // Simplified
		},
	)

	dbNs := createMockPackage(
		map[string]type_system.Type{},
		map[string]type_system.Type{
			"Connection": type_system.NewAnyType(nil), // Simplified
		},
	)

	configNs := createMockPackage(
		map[string]type_system.Type{},
		map[string]type_system.Type{
			"Settings": type_system.NewAnyType(nil), // Simplified
		},
	)

	require.NoError(t, c.PackageRegistry.Register("validator", validatorNs))
	require.NoError(t, c.PackageRegistry.Register("database", dbNs))
	require.NoError(t, c.PackageRegistry.Register("config", configNs))

	inferErrors := c.InferModule(inferCtx, module)

	for i, err := range inferErrors {
		t.Logf("Error[%d]: %s", i, err.Message())
	}

	assert.Empty(t, inferErrors, "Should infer complex project without errors")

	// Verify all types exist
	scope := inferCtx.Scope.Namespace

	expectedTypes := []string{
		"ID", "Timestamp", "BaseModel",
		"User", "UserValidation",
		"Post", "Comment",
		"UserService", "PostService",
		"App", "Array", // Local Array shadows global
	}

	for _, typeName := range expectedTypes {
		_, exists := scope.Types[typeName]
		assert.True(t, exists, "%s type should exist", typeName)
	}

	// Verify functions exist
	expectedFuncs := []string{
		"createUser", "getUserById",
		"createPost", "getPostsByUser",
		"initApp",
	}

	for _, funcName := range expectedFuncs {
		_, exists := scope.Values[funcName]
		assert.True(t, exists, "%s function should exist", funcName)
	}

	// Verify local vs global Array
	customArrayBinding, customExists := scope.Values["customArray"]
	globalArrayBinding, globalExists := scope.Values["globalArray"]

	assert.True(t, customExists, "customArray should exist")
	assert.True(t, globalExists, "globalArray should exist")

	if customExists {
		assert.Equal(t, "Array<string>", customArrayBinding.Type.String(), "Should use local Array type")
	}
	if globalExists {
		assert.Contains(t, globalArrayBinding.Type.String(), "globalThis.Array", "Should use global Array type")
	}
}

// TestE2E_RealisticMonorepoStructure simulates a package within a monorepo
func TestE2E_RealisticMonorepoStructure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Simulating @repo/core package - all files in same directory
	// NOTE: Using lib/ prefix to match the expected module structure
	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/types.esc",
			Contents: `
				// Core types shared across the monorepo
				type Result<T, E> = { ok: true, value: T } | { ok: false, error: E }
				type AsyncResult<T, E> = globalThis.Promise<Result<T, E>>
			`,
		},
		{
			ID:   1,
			Path: "lib/errors.esc",
			Contents: `
				// Error types
				type AppError = {
					code: string,
					message: string,
					stack: string,
				}

				type ValidationError = AppError & {
					field: string,
					constraint: string,
				}
			`,
		},
		{
			ID:   2,
			Path: "lib/utils.esc",
			Contents: `
				import * as lodash from "lodash"

				// Utility functions using lodash
				declare fn deepClone<T>(obj: T) -> T
				declare fn merge<T>(target: T, source: T) -> T

				// Type alias using imported package
				type DeepPartial<T> = T
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	require.Empty(t, parseErrors, "Should parse without errors")

	c := NewChecker()

	// IMPORTANT: Call Prelude FIRST, then register packages.
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}

	// Register lodash
	lodashNs := createMockPackage(
		map[string]type_system.Type{
			"cloneDeep": type_system.NewFuncType(nil, nil,
				[]*type_system.FuncParam{type_system.NewFuncParam(type_system.NewIdentPat("value"), type_system.NewAnyType(nil))},
				type_system.NewAnyType(nil),
				type_system.NewNeverType(nil),
			),
			"merge": type_system.NewFuncType(nil, nil,
				[]*type_system.FuncParam{
					type_system.NewFuncParam(type_system.NewIdentPat("object"), type_system.NewAnyType(nil)),
					type_system.NewFuncParam(type_system.NewIdentPat("sources"), type_system.NewAnyType(nil)),
				},
				type_system.NewAnyType(nil),
				type_system.NewNeverType(nil),
			),
		},
		nil,
	)

	require.NoError(t, c.PackageRegistry.Register("lodash", lodashNs))

	inferErrors := c.InferModule(inferCtx, module)

	for i, err := range inferErrors {
		t.Logf("Error[%d]: %s", i, err.Message())
	}

	assert.Empty(t, inferErrors, "Should infer monorepo structure without errors")

	// Verify types
	scope := inferCtx.Scope.Namespace
	expectedTypes := []string{"Result", "AsyncResult", "AppError", "ValidationError", "DeepPartial"}

	for _, typeName := range expectedTypes {
		_, exists := scope.Types[typeName]
		assert.True(t, exists, "%s type should exist", typeName)
	}
}

// TestE2E_GlobalAugmentation tests that global augmentations work correctly
// This is a placeholder for when declare global { ... } is fully supported
func TestE2E_GlobalAugmentation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// For now, just verify that globals from prelude are accessible
	// and that local declarations can shadow them
	// Note: We use Promise instead of Map because Map may not be in the prelude
	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/main.esc",
			Contents: `
				// Access global types from prelude
				declare val myArray: Array<number>
				declare val myPromise: Promise<string>

				// Shadow a global and verify both are accessible
				type Promise<T> = { customPromise: T, isPending: boolean }
				declare val customPromise: Promise<string>
				declare val globalPromise: globalThis.Promise<string>
			`,
		},
	}

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	require.Empty(t, parseErrors, "Should parse without errors")

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

	scope := inferCtx.Scope.Namespace

	// Verify global types work
	_, myArrayExists := scope.Values["myArray"]
	_, myPromiseExists := scope.Values["myPromise"]

	assert.True(t, myArrayExists, "myArray should exist")
	assert.True(t, myPromiseExists, "myPromise should exist")

	// Verify local Promise shadows global
	customPromiseBinding, customPromiseExists := scope.Values["customPromise"]
	globalPromiseBinding, globalPromiseExists := scope.Values["globalPromise"]

	assert.True(t, customPromiseExists, "customPromise should exist")
	assert.True(t, globalPromiseExists, "globalPromise should exist")

	if customPromiseExists {
		// Should reference local Promise type
		assert.Equal(t, "Promise<string>", customPromiseBinding.Type.String())
	}

	if globalPromiseExists {
		// Should reference globalThis.Promise
		assert.Contains(t, globalPromiseBinding.Type.String(), "globalThis.Promise")
	}
}
