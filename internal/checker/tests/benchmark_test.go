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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := 0; i < b.N; i++ {
		c := NewChecker(ctx)
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
		c := NewChecker(ctx)
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
		c := NewChecker(ctx)
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
		c := NewChecker(ctx)
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
		c := NewChecker(ctx)
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
		c := NewChecker(ctx)
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
		c := NewChecker(ctx)
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
		c := NewChecker(ctx)
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := NewChecker(ctx)
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := NewChecker(ctx)

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

// =============================================================================
// getMemberType Expand-Cache Benchmarks
// These benchmarks exercise repeated getMemberType calls on the same concrete
// generic types to measure the benefit of cross-call expand caching (#453).
//
// Each benchmark reuses a single Checker across the b.N loop so the cache
// can accumulate hits.
// =============================================================================

// BenchmarkRepeatedGenericPropertyAccess measures the cost of accessing
// multiple properties on the same concrete generic type (e.g. Array<number>)
// across many expressions within a single inference pass.
func BenchmarkRepeatedGenericPropertyAccess(b *testing.B) {
	// Access .length, then index, then method on the same Array<number> many times.
	// Each property access triggers getMemberType → ExpandType on Array<number>.
	input := `
		declare val a1: Array<number>
		declare val a2: Array<number>
		declare val a3: Array<number>
		declare val a4: Array<number>
		declare val a5: Array<number>

		val len1 = a1.length
		val len2 = a2.length
		val len3 = a3.length
		val len4 = a4.length
		val len5 = a5.length

		val inc1 = a1.includes(1)
		val inc2 = a2.includes(2)
		val inc3 = a3.includes(3)
		val inc4 = a4.includes(4)
		val inc5 = a5.includes(5)

		val idx1 = a1.indexOf(1)
		val idx2 = a2.indexOf(2)
		val idx3 = a3.indexOf(3)
		val idx4 = a4.indexOf(4)
		val idx5 = a5.indexOf(5)
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
		c := NewChecker(ctx)
		inferCtx := Context{
			Scope:   Prelude(c),
			IsAsync: false,
		}
		_, _ = c.InferScript(inferCtx, script)
	}
}

// BenchmarkMultipleGenericTypes measures the cost when several different
// concrete generic types (Array<string>, Map<string, number>, Set<boolean>,
// Promise<string>) are each accessed multiple times.
func BenchmarkMultipleGenericTypes(b *testing.B) {
	input := `
		declare val arr1: Array<string>
		declare val arr2: Array<string>
		declare val arr3: Array<string>

		declare val map1: Map<string, number>
		declare val map2: Map<string, number>
		declare val map3: Map<string, number>

		declare val set1: Set<boolean>
		declare val set2: Set<boolean>
		declare val set3: Set<boolean>

		declare val p1: Promise<string>
		declare val p2: Promise<string>
		declare val p3: Promise<string>

		val al1 = arr1.length
		val al2 = arr2.length
		val al3 = arr3.length

		val ms1 = map1.size
		val ms2 = map2.size
		val ms3 = map3.size

		val ss1 = set1.size
		val ss2 = set2.size
		val ss3 = set3.size

		val pt1 = p1.then(fn (x) => x)
		val pt2 = p2.then(fn (x) => x)
		val pt3 = p3.then(fn (x) => x)
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
		c := NewChecker(ctx)
		inferCtx := Context{
			Scope:   Prelude(c),
			IsAsync: false,
		}
		_, _ = c.InferScript(inferCtx, script)
	}
}

// BenchmarkUnionMemberAccess measures the cost of property access on union
// types where getMemberType is called recursively for each union member.
func BenchmarkUnionMemberAccess(b *testing.B) {
	input := `
		declare val u1: Array<number> | Array<string>
		declare val u2: Array<number> | Array<string>
		declare val u3: Array<number> | Array<string>
		declare val u4: Array<number> | Array<string>
		declare val u5: Array<number> | Array<string>

		val l1 = u1.length
		val l2 = u2.length
		val l3 = u3.length
		val l4 = u4.length
		val l5 = u5.length

		val i1 = u1.indexOf
		val i2 = u2.indexOf
		val i3 = u3.indexOf
		val i4 = u4.indexOf
		val i5 = u5.indexOf
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
		c := NewChecker(ctx)
		inferCtx := Context{
			Scope:   Prelude(c),
			IsAsync: false,
		}
		_, _ = c.InferScript(inferCtx, script)
	}
}

// BenchmarkGenericPropertyAccessModule measures expand cache effectiveness in
// a multi-file module where the same generic types appear across files.
func BenchmarkGenericPropertyAccessModule(b *testing.B) {
	sources := []*ast.Source{
		{
			ID:   0,
			Path: "lib/arrays.esc",
			Contents: `
				declare val items: Array<number>
				declare val names: Array<string>
				val itemLen = items.length
				val nameLen = names.length
				val hasItem = items.includes(1)
				val hasName = names.includes("a")
			`,
		},
		{
			ID:   1,
			Path: "lib/maps.esc",
			Contents: `
				declare val config: Map<string, number>
				declare val cache: Map<string, number>
				val configSize = config.size
				val cacheSize = cache.size
				val hasKey1 = config.has("a")
				val hasKey2 = cache.has("b")
			`,
		},
		{
			ID:   2,
			Path: "lib/promises.esc",
			Contents: `
				declare val p1: Promise<string>
				declare val p2: Promise<string>
				declare val p3: Promise<number>
				declare val p4: Promise<number>
				val t1 = p1.then(fn (x) => x)
				val t2 = p2.then(fn (x) => x)
				val t3 = p3.then(fn (x) => x)
				val t4 = p4.then(fn (x) => x)
			`,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	module, _ := parser.ParseLibFiles(ctx, sources)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c := NewChecker(ctx)
		inferCtx := Context{
			Scope:   Prelude(c),
			IsAsync: false,
		}
		_ = c.InferModule(inferCtx, module)
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
		c := NewChecker(ctx)
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

// TestArrayTypeStructure verifies where Array's properties live (Elems vs Extends)
// to inform lazy member lookup optimization decisions (#461).
func TestArrayTypeStructure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewChecker(ctx)
	scope := Prelude(c)
	alias := scope.GetTypeAlias("Array")
	if alias == nil {
		t.Fatal("Array type alias not found")
	}
	objType, ok := type_system.Prune(alias.Type).(*type_system.ObjectType)
	if !ok {
		t.Fatalf("Array type is %T, not ObjectType", type_system.Prune(alias.Type))
	}

	// Array should be a nominal interface with many elements and no Extends.
	if !objType.Nominal || !objType.Interface {
		t.Errorf("expected Nominal=true, Interface=true; got Nominal=%v, Interface=%v",
			objType.Nominal, objType.Interface)
	}
	if len(objType.Elems) == 0 {
		t.Error("expected Array to have elements")
	}

	// Verify common properties are findable in direct Elems.
	for _, name := range []string{"length", "indexOf", "join", "slice"} {
		key := type_system.NewStrKey(name)
		found := false
		for _, elem := range objType.Elems {
			switch e := elem.(type) {
			case *type_system.PropertyElem:
				if e.Name == key {
					found = true
				}
			case *type_system.MethodElem:
				if e.Name == key {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("expected %q in Array's direct Elems", name)
		}
	}
}
