package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// =============================================================================
// Performance Benchmarks
// These benchmarks measure type resolution performance to ensure no significant
// regression from the package/global namespace separation changes.
//
// Run benchmarks with: go test -bench=. -benchmem ./internal/checker/tests/
// =============================================================================

// BenchmarkPreludeLoading measures the time to initialize the global scope
// with all built-in types from lib.es5.d.ts and lib.dom.d.ts
func BenchmarkPreludeLoading(b *testing.B) {
	for i := 0; i < b.N; i++ {
		c := NewChecker()
		_ = Prelude(c)
	}
}

// BenchmarkSimpleScript measures inference time for a simple script
func BenchmarkSimpleScript(b *testing.B) {
	input := `
		val x = 5
		val y = "hello"
		val z = x + 10
		val arr = [1, 2, 3]
	`

	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: input,
	}

	// Parse once outside the benchmark loop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	script, _ := p.ParseScript()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c := NewChecker()
		inferCtx := Context{
			Scope:   Prelude(c),
			IsAsync: false,
		}
		_, _ = c.InferScript(inferCtx, script)
	}
}

// BenchmarkScriptWithGlobalTypes measures inference time when using global types
func BenchmarkScriptWithGlobalTypes(b *testing.B) {
	input := `
		declare val arr: Array<number>
		declare val promise: Promise<string>
		declare val map: Map<string, number>
		declare val set: Set<boolean>
		declare val date: Date
	`

	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: input,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	script, _ := p.ParseScript()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c := NewChecker()
		inferCtx := Context{
			Scope:   Prelude(c),
			IsAsync: false,
		}
		_, _ = c.InferScript(inferCtx, script)
	}
}

// BenchmarkScriptWithShadowing measures inference time when shadowing globals
func BenchmarkScriptWithShadowing(b *testing.B) {
	input := `
		type Array<T> = { items: T, isLocal: boolean }
		type Number = { value: number, isLocal: boolean }
		type String = { text: string, isLocal: boolean }

		declare val localArr: Array<number>
		declare val localNum: Number
		declare val localStr: String

		declare val globalArr: globalThis.Array<number>
		declare val globalPromise: globalThis.Promise<string>
	`

	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: input,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	script, _ := p.ParseScript()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c := NewChecker()
		inferCtx := Context{
			Scope:   Prelude(c),
			IsAsync: false,
		}
		_, _ = c.InferScript(inferCtx, script)
	}
}

// BenchmarkModuleWithImports measures inference time for a module with package imports
func BenchmarkModuleWithImports(b *testing.B) {
	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/main.esc",
			Contents: `
				import * as pkg1 from "package-1"
				import * as pkg2 from "package-2"
				import * as pkg3 from "package-3"

				declare val v1: pkg1.Type1
				declare val v2: pkg2.Type2
				declare val v3: pkg3.Type3
			`,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	module, _ := parser.ParseLibFiles(ctx, sources)

	// Create mock packages
	pkg1Ns := createMockPackage(nil, map[string]type_system.Type{"Type1": type_system.NewNumPrimType(nil)})
	pkg2Ns := createMockPackage(nil, map[string]type_system.Type{"Type2": type_system.NewStrPrimType(nil)})
	pkg3Ns := createMockPackage(nil, map[string]type_system.Type{"Type3": type_system.NewBoolPrimType(nil)})

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c := NewChecker()
		_ = c.PackageRegistry.Register("package-1", pkg1Ns)
		_ = c.PackageRegistry.Register("package-2", pkg2Ns)
		_ = c.PackageRegistry.Register("package-3", pkg3Ns)

		inferCtx := Context{
			Scope:   Prelude(c),
			IsAsync: false,
		}
		_ = c.InferModule(inferCtx, module)
	}
}

// BenchmarkMultiFileModule measures inference time for a multi-file module
func BenchmarkMultiFileModule(b *testing.B) {
	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/types.esc",
			Contents: `
				type ID = string
				type Timestamp = number
				type BaseModel = { id: ID, createdAt: Timestamp }
			`,
		},
		{
			ID:   1,
			Path: "lib/user.esc",
			Contents: `
				type User = BaseModel & { name: string, email: string }
			`,
		},
		{
			ID:   2,
			Path: "lib/post.esc",
			Contents: `
				type Post = BaseModel & { title: string, author: User }
			`,
		},
		{
			ID:   3,
			Path: "lib/comment.esc",
			Contents: `
				type Comment = BaseModel & { text: string, post: Post, author: User }
			`,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	module, _ := parser.ParseLibFiles(ctx, sources)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c := NewChecker()
		inferCtx := Context{
			Scope:   Prelude(c),
			IsAsync: false,
		}
		_ = c.InferModule(inferCtx, module)
	}
}

// BenchmarkCrossFileCyclicTypes measures inference time for cyclic type dependencies
func BenchmarkCrossFileCyclicTypes(b *testing.B) {
	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/node.esc",
			Contents: `
				type Node<T> = { value: T, children: Tree<T> }
			`,
		},
		{
			ID:   1,
			Path: "lib/tree.esc",
			Contents: `
				type Tree<T> = { root: Node<T>, size: number }
			`,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	module, _ := parser.ParseLibFiles(ctx, sources)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c := NewChecker()
		inferCtx := Context{
			Scope:   Prelude(c),
			IsAsync: false,
		}
		_ = c.InferModule(inferCtx, module)
	}
}

// BenchmarkFileScopedImports measures inference time for file-scoped imports
func BenchmarkFileScopedImports(b *testing.B) {
	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/file1.esc",
			Contents: `
				import * as utils from "shared-utils"
				declare val v1: utils.SharedType
			`,
		},
		{
			ID:   1,
			Path: "lib/file2.esc",
			Contents: `
				import * as utils from "shared-utils"
				declare val v2: utils.SharedType
			`,
		},
		{
			ID:   2,
			Path: "lib/file3.esc",
			Contents: `
				import * as utils from "shared-utils"
				declare val v3: utils.SharedType
			`,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	module, _ := parser.ParseLibFiles(ctx, sources)

	utilsNs := createMockPackage(nil, map[string]type_system.Type{"SharedType": type_system.NewNumPrimType(nil)})

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c := NewChecker()
		_ = c.PackageRegistry.Register("shared-utils", utilsNs)

		inferCtx := Context{
			Scope:   Prelude(c),
			IsAsync: false,
		}
		_ = c.InferModule(inferCtx, module)
	}
}

// BenchmarkScopeChainLookup measures the cost of scope chain traversal
func BenchmarkScopeChainLookup(b *testing.B) {
	c := NewChecker()
	userScope := Prelude(c)

	// Add local bindings to simulate a realistic scope
	for i := 0; i < 50; i++ {
		userScope.Namespace.Values[fmt.Sprintf("localVar%d", i)] = &type_system.Binding{
			Type:    type_system.NewNumPrimType(nil),
			Mutable: false,
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Lookup a global type through the scope chain
		_ = userScope.GetTypeAlias("Array")
		_ = userScope.GetTypeAlias("Promise")
		_ = userScope.GetTypeAlias("Map")
		_ = userScope.GetValue("globalThis")
	}
}

// BenchmarkPackageRegistryLookup measures the cost of package registry lookups
func BenchmarkPackageRegistryLookup(b *testing.B) {
	c := NewChecker()

	// Register several packages
	for i := 0; i < 20; i++ {
		pkgName := "package-" + string(rune('a'+i))
		ns := type_system.NewNamespace()
		ns.Types["Type"] = &type_system.TypeAlias{
			Type:       type_system.NewNumPrimType(nil),
			TypeParams: nil,
		}
		_ = c.PackageRegistry.Register(pkgName, ns)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Lookup packages
		_, _ = c.PackageRegistry.Lookup("package-a")
		_, _ = c.PackageRegistry.Lookup("package-j")
		_, _ = c.PackageRegistry.Lookup("package-t")
		_ = c.PackageRegistry.Has("package-nonexistent")
	}
}

// BenchmarkComplexProject simulates inference for a larger project
func BenchmarkComplexProject(b *testing.B) {
	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/models/base.esc",
			Contents: `
				type ID = string
				type Timestamp = number
				type BaseModel = { id: ID, createdAt: Timestamp, updatedAt: Timestamp }
			`,
		},
		{
			ID:   1,
			Path: "lib/models/user.esc",
			Contents: `
				import * as validator from "validator"
				type User = BaseModel & { email: string, name: string }
				type UserValidation = { emailValid: validator.Result, nameValid: validator.Result }
			`,
		},
		{
			ID:   2,
			Path: "lib/models/post.esc",
			Contents: `
				type Post = BaseModel & { title: string, content: string, author: User }
			`,
		},
		{
			ID:   3,
			Path: "lib/models/comment.esc",
			Contents: `
				type Comment = BaseModel & { text: string, post: Post, author: User }
			`,
		},
		{
			ID:   4,
			Path: "lib/services/user_service.esc",
			Contents: `
				import * as db from "database"
				type UserService = { db: db.Connection }
				declare fn createUser(s: UserService, u: User) -> globalThis.Promise<User>
				declare fn getUser(s: UserService, id: ID) -> globalThis.Promise<User>
			`,
		},
		{
			ID:   5,
			Path: "lib/services/post_service.esc",
			Contents: `
				import * as db from "database"
				type PostService = { db: db.Connection }
				declare fn createPost(s: PostService, p: Post) -> globalThis.Promise<Post>
				declare fn getPosts(s: PostService, userId: ID) -> globalThis.Promise<globalThis.Array<Post>>
			`,
		},
		{
			ID:   6,
			Path: "lib/app.esc",
			Contents: `
				import * as config from "config"
				type App = { config: config.Settings, userService: UserService, postService: PostService }
				declare fn initApp(cfg: config.Settings) -> globalThis.Promise<App>
			`,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	module, _ := parser.ParseLibFiles(ctx, sources)

	validatorNs := createMockPackage(nil, map[string]type_system.Type{"Result": type_system.NewAnyType(nil)})
	dbNs := createMockPackage(nil, map[string]type_system.Type{"Connection": type_system.NewAnyType(nil)})
	configNs := createMockPackage(nil, map[string]type_system.Type{"Settings": type_system.NewAnyType(nil)})

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c := NewChecker()
		_ = c.PackageRegistry.Register("validator", validatorNs)
		_ = c.PackageRegistry.Register("database", dbNs)
		_ = c.PackageRegistry.Register("config", configNs)

		inferCtx := Context{
			Scope:   Prelude(c),
			IsAsync: false,
		}
		_ = c.InferModule(inferCtx, module)
	}
}
